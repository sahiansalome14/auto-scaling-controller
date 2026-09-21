// config.go  tipos compartidos, configuracion y validacion estructural

package main

import (
	"fmt"
	"os"
	"time"

	"gopkg.in/yaml.v3"
)

// Tipos de decision y calidad de datos
type Decision string

const (
	MaintainCapacity Decision = "MAINTAIN_CAPACITY"
	IncreaseCapacity Decision = "INCREASE_CAPACITY"
	ReduceCapacity   Decision = "REDUCE_CAPACITY"
)

type Quality string

const (
	DataOK           Quality = "OK"
	DataInsufficient Quality = "INSUFFICIENT"
)

const (
	ReasonDataInsufficient = "DATA_INSUFFICIENT"
	ReasonCapacityUnstable = "CAPACITY_UNSTABLE"
	ReasonCooldown         = "COOLDOWN"
	ReasonSustainedHigh    = "SUSTAINED_HIGH"
	ReasonSaturatedAtMax   = "SATURATED_AT_MAX"
	ReasonSustainedLow     = "SUSTAINED_LOW"
	ReasonAtMin            = "AT_MIN_CAPACITY"
	ReasonUnsafeToReduce   = "UNSAFE_TO_REDUCE"
	ReasonWithinBand       = "WITHIN_BAND"
)

// Nombres de las metricas de CloudWatch que el controlador observa.
const (
	MetricCPU        = "cpu_utilization"
	MetricLatencyP90 = "target_response_time_p90"
	MetricRPT        = "request_count_per_target"
)

// Estructuras de datos
type Window struct {
	Start   time.Time `json:"start"`
	End     time.Time `json:"end"`
	Seconds int       `json:"seconds"`
}

type Metric struct {
	Value      float64   `json:"value"`
	Samples    []float64 `json:"samples"`
	LatestAt   time.Time `json:"latest_datapoint_at"`
	AgeSeconds float64   `json:"age_seconds"`
	Source     string    `json:"source"`
}

type Capacity struct {
	Desired        int `json:"desired"`
	InService      int `json:"in_service"`
	Pending        int `json:"pending"`
	Terminating    int `json:"terminating"`
	HealthyTargets int `json:"healthy_targets"`
}

type Observation struct {
	Window    Window
	Metrics   map[string]*Metric
	Capacity  Capacity
	Discarded []string
}

func (o *Observation) get(name string) *Metric {
	if o == nil || o.Metrics == nil {
		return nil
	}
	return o.Metrics[name]
}

type Action struct {
	From int `json:"from"`
	To   int `json:"to"`
}

type Result struct {
	Status    string `json:"status"` // NO_ACTION, DRY_RUN, SUCCEEDED, FAILED
	LatencyMS int64  `json:"latency_ms"`
	Error     string `json:"error,omitempty"`
}


// Contiene todo lo necesario para reconstruir por que se tomo la decision.
type Record struct {
	Cycle        int64  `json:"cycle"`
	ExperimentID string `json:"experiment_id"`
	Params       any    `json:"params"`

	Timestamp time.Time          `json:"timestamp"`
	Window    Window             `json:"observation_window"`
	Metrics   map[string]*Metric `json:"metrics"`
	Quality   Quality            `json:"data_quality"`
	Discarded []string           `json:"discarded_datapoints"`
	Capacity  Capacity           `json:"capacity"`

	CooldownActive         bool    `json:"cooldown_active"`
	SecondsSinceLastAction float64 `json:"seconds_since_last_action"`

	Decision   Decision `json:"decision"`
	ReasonCode string   `json:"reason_code"`
	Reason     string   `json:"reason"`
	Action     *Action  `json:"requested_action,omitempty"`
	Result     Result   `json:"result"`
}


type AWSConfig struct {
	Region           string `yaml:"region"`
	AutoScalingGroup string `yaml:"auto_scaling_group"`
	TargetGroupARN   string `yaml:"target_group_arn"`
	LoadBalancerDim  string `yaml:"load_balancer_dimension"`
	TargetGroupDim   string `yaml:"target_group_dimension"`
	APIMaxAttempts   int    `yaml:"api_max_attempts"`
}

type Params struct {
	LoopIntervalSeconds      int `yaml:"loop_interval_seconds"      json:"loop_interval_seconds"`
	ObservationWindowSeconds int `yaml:"observation_window_seconds" json:"observation_window_seconds"`
	AggregationPeriodSeconds int `yaml:"aggregation_period_seconds" json:"aggregation_period_seconds"`
	NSamples                 int `yaml:"n_samples"                  json:"n_samples"`

	UHigh float64 `yaml:"u_high" json:"u_high"`
	ULow  float64 `yaml:"u_low"  json:"u_low"`
	KUp   int     `yaml:"k_up"   json:"k_up"`
	KDown int     `yaml:"k_down" json:"k_down"`

	CooldownSeconds int `yaml:"cooldown_seconds" json:"cooldown_seconds"`

	Step        int `yaml:"step"         json:"step"`
	MinCapacity int `yaml:"min_capacity" json:"min_capacity"`
	MaxCapacity int `yaml:"max_capacity" json:"max_capacity"`

	MetricFreshnessSeconds    int     `yaml:"metric_freshness_seconds"     json:"metric_freshness_seconds"`
	ExpectedPublishLagSeconds int     `yaml:"expected_publish_lag_seconds" json:"expected_publish_lag_seconds"`
	ReduceMaxP90Seconds       float64 `yaml:"reduce_max_p90_seconds"       json:"reduce_max_p90_seconds"`
}

func (p Params) LoopInterval() time.Duration { return time.Duration(p.LoopIntervalSeconds) * time.Second }
func (p Params) Window() time.Duration       { return time.Duration(p.ObservationWindowSeconds) * time.Second }
func (p Params) Cooldown() time.Duration     { return time.Duration(p.CooldownSeconds) * time.Second }
func (p Params) Freshness() time.Duration    { return time.Duration(p.MetricFreshnessSeconds) * time.Second }

type Config struct {
	AWS          AWSConfig `yaml:"aws"`
	Params       Params    `yaml:"params"`
	DecisionLog  string    `yaml:"decision_log_path"`
	ExperimentID string    `yaml:"experiment_id"`
}

func loadConfig(path string) (*Config, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("leyendo configuracion: %w", err)
	}
	var c Config
	if err := yaml.Unmarshal(raw, &c); err != nil {
		return nil, fmt.Errorf("parseando configuracion: %w", err)
	}
	if err := c.Validate(); err != nil {
		return nil, err
	}
	return &c, nil
}

// impone las condiciones estructurales del diseño
func (c *Config) Validate() error {
	p := c.Params
	switch {
	case p.MinCapacity < 1:
		return fmt.Errorf("min_capacity debe ser >= 1")
	case p.MaxCapacity > 5:
		return fmt.Errorf("max_capacity debe ser <= 5")
	case p.MinCapacity > p.MaxCapacity:
		return fmt.Errorf("min_capacity > max_capacity")
	case p.Step < 1:
		return fmt.Errorf("step debe ser >= 1")
	case p.ULow >= p.UHigh:
		return fmt.Errorf("u_low (%.1f) debe ser menor que u_high (%.1f)", p.ULow, p.UHigh)
	case p.KUp < 1 || p.KDown <= p.KUp:
		return fmt.Errorf("k_down (%d) debe ser mayor que k_up (%d): bajar exige mas evidencia", p.KDown, p.KUp)
	case p.KDown > p.NSamples:
		return fmt.Errorf("k_down (%d) no puede exceder n_samples (%d)", p.KDown, p.NSamples)
	}

	// Anti-oscilacion
	n := float64(p.MinCapacity)
	if after := p.UHigh * n / (n + float64(p.Step)); after <= p.ULow {
		return fmt.Errorf("banda muerta incoherente: tras subir desde %d instancias la CPU "+
			"esperada (%.1f%%) queda bajo u_low (%.1f%%); requiere u_low < %.1f",
			p.MinCapacity, after, p.ULow, after)
	}

	// Ventana minima
	need := p.NSamples*p.AggregationPeriodSeconds + p.ExpectedPublishLagSeconds
	if p.ObservationWindowSeconds < need {
		return fmt.Errorf("observation_window (%ds) insuficiente: necesita al menos %ds", p.ObservationWindowSeconds, need)
	}
	return nil
}
