// lee metricas y capacidad de AWS, descarta datapoints imposibles y decide si los datos alcanzan para decidir

package main

import (
	"context"
	"fmt"
	"math"
	"time"
)

// Devuelve error si falla cualquier llamada a AWS, se queda en mantain
func observe(ctx context.Context, clients *Clients, cfg *Config, now time.Time) (*Observation, Quality, error) {
	p := cfg.Params
	obs := &Observation{
		Window:  Window{Start: now.Add(-p.Window()), End: now, Seconds: p.ObservationWindowSeconds},
		Metrics: map[string]*Metric{},
	}

	capacity, err := fetchCapacity(ctx, clients, cfg)
	if err != nil {
		return nil, DataInsufficient, fmt.Errorf("observando capacidad: %w", err)
	}
	obs.Capacity = capacity

	metrics, err := fetchMetrics(ctx, clients, cfg, obs.Window.Start, now)
	if err != nil {
		return obs, DataInsufficient, fmt.Errorf("observando metricas: %w", err)
	}
	obs.Metrics = metrics
	return obs, validateMetrics(obs, p, now), nil
}

// Valida, filtra y procesa las métricas leídas sin tocar AWS
func validateMetrics(obs *Observation, p Params, now time.Time) Quality {
	for name, m := range obs.Metrics {
		if m == nil {
			delete(obs.Metrics, name)
			continue
		}
		kept := make([]float64, 0, len(m.Samples))
		for _, v := range m.Samples {
			if reason := invalidDatapoint(name, v); reason != "" {
				obs.Discarded = append(obs.Discarded, fmt.Sprintf("%s=%v: %s", name, v, reason))
				continue
			}
			kept = append(kept, v)
		}
		m.Samples = kept
		m.Value = mean(kept)
		m.AgeSeconds = now.Sub(m.LatestAt).Seconds()
	}

	cpu := obs.get(MetricCPU)
	switch {
	case cpu == nil, len(cpu.Samples) < p.KUp:
		return DataInsufficient // muy pocas muestras validas
	case cpu.LatestAt.IsZero(), cpu.AgeSeconds > p.Freshness().Seconds():
		return DataInsufficient // dato demasiado viejo
	}
	return DataOK
}

// devuelve el motivo si el valor es fisicamente imposible
func invalidDatapoint(metric string, v float64) string {
	switch {
	case math.IsNaN(v) || math.IsInf(v, 0):
		return "no numerico"
	case v < 0:
		return "negativo"
	case metric == MetricCPU && v > 100:
		return "CPU > 100%"
	}
	return ""
}
