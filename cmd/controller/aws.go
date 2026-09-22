// Todo el conocimiento de AWS vive aqui
package main

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awscfg "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/autoscaling"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatch"
	cwtypes "github.com/aws/aws-sdk-go-v2/service/cloudwatch/types"
	"github.com/aws/aws-sdk-go-v2/service/elasticloadbalancingv2"
	elbtypes "github.com/aws/aws-sdk-go-v2/service/elasticloadbalancingv2/types"
)


// Las credenciales vienen del Instance Profile nunca claves en el repositorio
type Clients struct {
	CW  *cloudwatch.Client
	ASG *autoscaling.Client
	ELB *elasticloadbalancingv2.Client
}

// inicializa los clientes de AWS con la configuracion y los reintentos del SDK
func newClients(ctx context.Context, cfg *Config) (*Clients, error) {
	awsConf, err := awscfg.LoadDefaultConfig(ctx,
		awscfg.WithRegion(cfg.AWS.Region),
		awscfg.WithRetryMaxAttempts(cfg.AWS.APIMaxAttempts),
	)
	if err != nil {
		return nil, fmt.Errorf("cargando configuracion de AWS: %w", err)
	}
	return &Clients{
		CW:  cloudwatch.NewFromConfig(awsConf),
		ASG: autoscaling.NewFromConfig(awsConf),
		ELB: elasticloadbalancingv2.NewFromConfig(awsConf),
	}, nil
}

// pide a CloudWatch las tres metricas de interes en la ventana dada
func fetchMetrics(ctx context.Context, clients *Clients, cfg *Config, start, end time.Time) (map[string]*Metric, error) {
	a := cfg.AWS
	period := int32(cfg.Params.AggregationPeriodSeconds)
	tg := map[string]string{"TargetGroup": a.TargetGroupDim, "LoadBalancer": a.LoadBalancerDim}

	makeQuery := func(id, ns, name, stat string, dims map[string]string) cwtypes.MetricDataQuery {
		var d []cwtypes.Dimension
		for k, v := range dims {
			d = append(d, cwtypes.Dimension{Name: aws.String(k), Value: aws.String(v)})
		}
		return cwtypes.MetricDataQuery{
			Id: aws.String(id),
			MetricStat: &cwtypes.MetricStat{
				Metric: &cwtypes.Metric{Namespace: aws.String(ns), MetricName: aws.String(name), Dimensions: d},
				Period: aws.Int32(period),
				Stat:   aws.String(stat),
			},
			ReturnData: aws.Bool(true),
		}
	}

	out, err := clients.CW.GetMetricData(ctx, &cloudwatch.GetMetricDataInput{
		StartTime: aws.Time(start),
		EndTime:   aws.Time(end),
		ScanBy:    cwtypes.ScanByTimestampAscending,
		MetricDataQueries: []cwtypes.MetricDataQuery{
			makeQuery("cpu", "AWS/EC2", "CPUUtilization", "Average",
				map[string]string{"AutoScalingGroupName": a.AutoScalingGroup}),
			makeQuery("lat", "AWS/ApplicationELB", "TargetResponseTime", "p90", tg),
			makeQuery("rpt", "AWS/ApplicationELB", "RequestCountPerTarget", "Sum", tg),
			makeQuery("e5x", "AWS/ApplicationELB", "HTTPCode_ELB_5XX_Count", "Sum", map[string]string{"LoadBalancer": a.LoadBalancerDim}),
		},
	})
	if err != nil {
		return nil, fmt.Errorf("GetMetricData: %w", err)
	}

	names := map[string]struct{ metric, source string }{
		"cpu": {MetricCPU, "AWS/EC2 CPUUtilization (AutoScalingGroupName)"},
		"lat": {MetricLatencyP90, "AWS/ApplicationELB TargetResponseTime p90"},
		"rpt": {MetricRPT, "AWS/ApplicationELB RequestCountPerTarget"},
		"e5x": {MetricHTTP5xx, "AWS/ApplicationELB HTTPCode_Target_5XX_Count"},
	}
	res := map[string]*Metric{}
	for _, r := range out.MetricDataResults {
		meta, ok := names[aws.ToString(r.Id)]
		if !ok {
			continue
		}
		m := &Metric{Source: meta.source, Samples: r.Values}
		if n := len(r.Timestamps); n > 0 {
			m.LatestAt = r.Timestamps[n-1].UTC()
		}
		res[meta.metric] = m
	}
	return res, nil
}

// lee el estado real del ASG y los targets sanos en el Target Group
func fetchCapacity(ctx context.Context, clients *Clients, cfg *Config) (Capacity, error) {
	a := cfg.AWS
	out, err := clients.ASG.DescribeAutoScalingGroups(ctx, &autoscaling.DescribeAutoScalingGroupsInput{
		AutoScalingGroupNames: []string{a.AutoScalingGroup},
	})
	if err != nil {
		return Capacity{}, fmt.Errorf("DescribeAutoScalingGroups: %w", err)
	}
	if len(out.AutoScalingGroups) == 0 {
		return Capacity{}, fmt.Errorf("el grupo %q no existe", a.AutoScalingGroup)
	}
	g := out.AutoScalingGroups[0]

	c := Capacity{Desired: int(aws.ToInt32(g.DesiredCapacity))}
	for _, inst := range g.Instances {
		s := string(inst.LifecycleState)
		switch {
		case s == "InService":
			c.InService++
		case strings.HasPrefix(s, "Pending"):
			c.Pending++
		case strings.HasPrefix(s, "Terminating"), s == "Detaching":
			c.Terminating++
		}
	}

	th, err := clients.ELB.DescribeTargetHealth(ctx, &elasticloadbalancingv2.DescribeTargetHealthInput{
		TargetGroupArn: aws.String(a.TargetGroupARN),
	})
	if err != nil {
		return Capacity{}, fmt.Errorf("DescribeTargetHealth: %w", err)
	}
	for _, t := range th.TargetHealthDescriptions {
		if t.TargetHealth != nil && t.TargetHealth.State == elbtypes.TargetHealthStateEnumHealthy {
			c.HealthyTargets++
		}
	}
	return c, nil
}

// le pide al ASG una nueva cantidad de instancias
func setDesiredCapacity(ctx context.Context, clients *Clients, cfg *Config, desired int) error {
	p := cfg.Params
	if desired < p.MinCapacity || desired > p.MaxCapacity {
		return fmt.Errorf("capacidad %d fuera del rango permitido [%d, %d]", desired, p.MinCapacity, p.MaxCapacity)
	}
	_, err := clients.ASG.SetDesiredCapacity(ctx, &autoscaling.SetDesiredCapacityInput{
		AutoScalingGroupName: aws.String(cfg.AWS.AutoScalingGroup),
		DesiredCapacity:      aws.Int32(int32(desired)),
		// elcooldown es responsabilidad del controlador, no del ASG
		HonorCooldown: aws.Bool(false),
	})
	if err != nil {
		return fmt.Errorf("SetDesiredCapacity(%d): %w", desired, err)
	}
	return nil
}
