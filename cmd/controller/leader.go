package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awscfg "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	ddbtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
)

// LeaderElector gestiona la eleccion de lider usando escrituras condicionales en DynamoDB.
type LeaderElector struct {
	client       *dynamodb.Client
	tableName    string
	controllerID string
	leaseTTL     time.Duration
}

func NewLeaderElector(ctx context.Context, region, tableName string, leaseTTL time.Duration) (*LeaderElector, error) {
	if tableName == "" {
		tableName = "controller-leader-lock"
	}
	if leaseTTL <= 0 {
		leaseTTL = 15 * time.Second
	}

	hostname := os.Getenv("CONTROLLER_ID")
	if hostname == "" {
		h, err := os.Hostname()
		if err != nil || h == "" {
			hostname = fmt.Sprintf("controller-%d", time.Now().UnixNano())
		} else {
			hostname = h
		}
	}

	awsConf, err := awscfg.LoadDefaultConfig(ctx, awscfg.WithRegion(region))
	if err != nil {
		return nil, fmt.Errorf("cargando configuracion de AWS para DynamoDB: %w", err)
	}

	return &LeaderElector{
		client:       dynamodb.NewFromConfig(awsConf),
		tableName:    tableName,
		controllerID: hostname,
		leaseTTL:     leaseTTL,
	}, nil
}

// TryAcquireOrRenew intenta obtener o renovar el liderazgo en DynamoDB.
// Retorna (true, nil) si esta instancia es el lider activo.
func (e *LeaderElector) TryAcquireOrRenew(ctx context.Context) (bool, error) {
	now := time.Now().UTC().Unix()
	expiresAt := now + int64(e.leaseTTL.Seconds())

	// Intenta escribir o actualizar el registro condicionalmente
	input := &dynamodb.PutItemInput{
		TableName: aws.String(e.tableName),
		Item: map[string]ddbtypes.AttributeValue{
			"LockID":    &ddbtypes.AttributeValueMemberS{Value: "autoscaling-controller-leader"},
			"LeaderID":  &ddbtypes.AttributeValueMemberS{Value: e.controllerID},
			"ExpiresAt": &ddbtypes.AttributeValueMemberN{Value: fmt.Sprintf("%d", expiresAt)},
		},
		// Regla condicional:
		// El lock se adquiere si:
		// 1. No existe (primera ejecucion)
		// 2. Ya expiro (el lider anterior murio o perdio conectividad)
		// 3. Yo soy el lider actual (renovacion de contrato/lease)
		ConditionExpression: aws.String("attribute_not_exists(LockID) OR ExpiresAt < :now OR LeaderID = :me"),
		ExpressionAttributeValues: map[string]ddbtypes.AttributeValue{
			":now": &ddbtypes.AttributeValueMemberN{Value: fmt.Sprintf("%d", now)},
			":me":  &ddbtypes.AttributeValueMemberS{Value: e.controllerID},
		},
	}

	_, err := e.client.PutItem(ctx, input)
	if err != nil {
		var checkFailed *ddbtypes.ConditionalCheckFailedException
		if errors.As(err, &checkFailed) {
			// Es el comportamiento esperado para un Follower en Standby: otra instancia es el Lider
			return false, nil
		}
		// Error real de red, credenciales o tabla inexistente
		return false, err
	}

	return true, nil
}
