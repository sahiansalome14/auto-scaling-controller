package main

import (
	"context"
	"time"
)

// CloudProvider define la interfaz abstracta para interactuar con la nube.
// Esto permite desacoplar el controlador de AWS
type CloudProvider interface {
	FetchCapacity(ctx context.Context, cfg *Config) (Capacity, error)
	FetchMetrics(ctx context.Context, cfg *Config, start, end time.Time) (map[string]*Metric, error)
	SetDesiredCapacity(ctx context.Context, cfg *Config, desired int) error
}
