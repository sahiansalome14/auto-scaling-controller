// logica de decision del controlador

// Sin datos suficientes         MAINTAIN  (un error nunca debe mover capacidad)
// Capacidad en transicion       MAINTAIN  (esperar el efecto de la accion anterior)
// Cooldown activo               MAINTAIN  (no decidir justo despues de escalar)
// CPU alta sostenida            INCREASE
// CPU baja sostenida y segura   REDUCE
// Cualquier otro caso           MAINTAIN  (dentro de la banda)

package main

import (
	"fmt"
	"math"
	"time"
)

// resultado de la politica: que hacer y por que
type Outcome struct {
	Decision   Decision
	Target     int // capacidad deseada tras la decision
	ReasonCode string
	Reason     string
}

// dice si aun rige el tiempo de espera tras la ultima accion
func cooldownActive(lastActionAt, now time.Time, p Params) bool {
	return !lastActionAt.IsZero() && now.Sub(lastActionAt) < p.Cooldown()
}

// decide implementa la cascada de decision descrita arriba
func decide(obs *Observation, quality Quality, lastActionAt, now time.Time, p Params) Outcome {
	c := obs.Capacity

	maintain := func(code, reason string) Outcome {
		return Outcome{Decision: MaintainCapacity, Target: c.Desired, ReasonCode: code, Reason: reason}
	}

	// datos insuficientes
	cpuMetric := obs.get(MetricCPU)
	if quality != DataOK || cpuMetric == nil {
		return maintain(ReasonDataInsufficient, "Datos insuficientes: se mantiene la capacidad (fail-safe).")
	}

	// instancias arrancando/terminando, o el ALB aun no registra todos los targets deseados como sanos.
	if c.Pending > 0 || c.Terminating > 0 {
		return maintain(ReasonCapacityUnstable, fmt.Sprintf(
			"Capacidad en transicion (deseada=%d, sanos=%d, pending=%d, terminating=%d).",
			c.Desired, c.HealthyTargets, c.Pending, c.Terminating))
	}

	// cooldown
	if cooldownActive(lastActionAt, now, p) {
		left := p.Cooldown() - now.Sub(lastActionAt)
		return maintain(ReasonCooldown, fmt.Sprintf("Cooldown activo (%.0fs restantes).", left.Seconds()))
	}

	// instancias ya establecidas (sin pending/terminating) que fallan el health check
	if c.HealthyTargets < c.Desired {
		detail := fmt.Sprintf("instancias establecidas fallan el health check (sanos=%d de deseadas=%d)",
			c.HealthyTargets, c.Desired)
		if c.Desired >= p.MaxCapacity {
			return maintain(ReasonSaturatedAtMax, detail+fmt.Sprintf(", ya en el maximo (%d).", p.MaxCapacity))
		}
		target := min(c.Desired+p.Step, p.MaxCapacity)
		return Outcome{Decision: IncreaseCapacity, Target: target, ReasonCode: ReasonUnhealthyEstablished,
			Reason: detail + fmt.Sprintf(": se sube de %d a %d.", c.Desired, target)}
	}

	last := lastN(cpuMetric.Samples, p.NSamples)

	// Alta tasa de errores 5xx
	// Suma HTTPCode_ELB_5XX_Count (servidor no acepta conexiones) y
	// HTTPCode_Target_5XX_Count (errores HTTP de la app) para capturar ambas causas.
	// El servidor rechaza conexiones aunque la CPU este dentro de la banda.
	if p.ErrorRateScaleUpPct > 0 {
		e5xELB := obs.get(MetricELB5xx)
		e5xTgt := obs.get(MetricTarget5xx)
		rpt := obs.get(MetricRPT)
		if rpt != nil && len(rpt.Samples) > 0 {
			// Contar el total de peticiones reales: RPT es por target sano
			healthyBase := c.HealthyTargets
			if healthyBase == 0 {
				healthyBase = c.Desired // fallback: si aun no hay targets sanos registrados
			}
			lastRpt := rpt.Samples[len(rpt.Samples)-1]
			totalRpt := lastRpt * float64(healthyBase)
			if totalRpt > 0 {
				// Sumar ambas fuentes de errores 5xx
				var total5xx float64
				if e5xELB != nil && len(e5xELB.Samples) > 0 {
					total5xx += e5xELB.Samples[len(e5xELB.Samples)-1]
				}
				if e5xTgt != nil && len(e5xTgt.Samples) > 0 {
					total5xx += e5xTgt.Samples[len(e5xTgt.Samples)-1]
				}
				if total5xx > 0 {
					errorRate := total5xx / totalRpt * 100
					if errorRate >= p.ErrorRateScaleUpPct {
						detail := fmt.Sprintf("tasa de errores 5xx (%.1f%%) supera el umbral (%.1f%%)",
							errorRate, p.ErrorRateScaleUpPct)
						if c.Desired >= p.MaxCapacity {
							return maintain(ReasonSaturatedAtMax, detail+fmt.Sprintf(", ya en el maximo (%d).", p.MaxCapacity))
						}
						target := min(c.Desired+p.Step, p.MaxCapacity)
						return Outcome{Decision: IncreaseCapacity, Target: target,
							ReasonCode: ReasonHighErrorRate,
							Reason:     detail + fmt.Sprintf(": se sube de %d a %d.", c.Desired, target)}
					}
				}
			}
		}
	}

	// Pre-escalado proactivo por pendiente de RPS 
	// Si la tasa de crecimiento de RequestCountPerTarget supera el umbral,
	// se escala sin esperar a que la CPU o los errores confirmen la saturacion.
	// Desactivado cuando rps_slope_threshold = 0.
	if p.RPSSlopeThreshold > 0 {
		rpt := obs.get(MetricRPT)
		if rpt != nil && len(rpt.Samples) >= 3 {
			slope := rpsSlope(rpt.Samples)
			if slope >= p.RPSSlopeThreshold {
				detail := fmt.Sprintf("pendiente de RPS (%.2f req/target/periodo) supera el umbral (%.2f)",
					slope, p.RPSSlopeThreshold)
				if c.Desired >= p.MaxCapacity {
					return maintain(ReasonSaturatedAtMax, detail+fmt.Sprintf(", ya en el maximo (%d).", p.MaxCapacity))
				}
				target := min(c.Desired+p.Step, p.MaxCapacity)
				return Outcome{Decision: IncreaseCapacity, Target: target,
					ReasonCode: ReasonProactiveRPSTrend,
					Reason:     detail + fmt.Sprintf(": se pre-escala de %d a %d.", c.Desired, target)}
			}
		}
	}

	// subida por CPU alta sostenida 
	// k_up de las ultimas n_samples muestras superan u_high
	if n := countIf(last, func(v float64) bool { return v > p.UHigh }); n >= p.KUp {
		detail := fmt.Sprintf("CPU > %.0f%% en %d de %d muestras (requiere %d)", p.UHigh, n, len(last), p.KUp)
		if c.Desired >= p.MaxCapacity {
			return maintain(ReasonSaturatedAtMax, detail+fmt.Sprintf(", ya en el maximo (%d).", p.MaxCapacity))
		}
		target := min(c.Desired+p.Step, p.MaxCapacity)
		return Outcome{Decision: IncreaseCapacity, Target: target, ReasonCode: ReasonSustainedHigh,
			Reason: detail + fmt.Sprintf(": se sube de %d a %d.", c.Desired, target)}
	}

	//  bajada por CPU baja sostenida
	// k_down de las ultimas n_samples muestras estan bajo u_low.
	// exige mas evidencia que subir y ademas verifica que es seguro reducir
	if n := countIf(last, func(v float64) bool { return v < p.ULow }); n >= p.KDown {
		detail := fmt.Sprintf("CPU < %.0f%% en %d de %d muestras (requiere %d)", p.ULow, n, len(last), p.KDown)
		if c.Desired <= p.MinCapacity {
			return maintain(ReasonAtMin, detail+fmt.Sprintf(", ya en el minimo (%d).", p.MinCapacity))
		}
		target := max(c.Desired-p.Step, p.MinCapacity)
		if ok, why := safeToReduce(obs, last, target, p); !ok {
			return maintain(ReasonUnsafeToReduce, detail+": reducir NO es seguro: "+why+".")
		}
		return Outcome{Decision: ReduceCapacity, Target: target, ReasonCode: ReasonSustainedLow,
			Reason: detail + fmt.Sprintf(" y la reduccion es segura: se baja de %d a %d.", c.Desired, target)}
	}

	// dentro de la banda
	return maintain(ReasonWithinBand, fmt.Sprintf(
		"CPU dentro de la banda [%.0f%%, %.0f%%] o sin evidencia sostenida.", p.ULow, p.UHigh))
}

// comprueba dos condiciones antes de autorizar una bajada de capacidad
func safeToReduce(obs *Observation, cpuLast []float64, target int, p Params) (bool, string) {
	desired := float64(obs.Capacity.Desired)
	projected := mean(cpuLast) * desired / float64(target)
	if projected >= p.UHigh {
		return false, fmt.Sprintf("la CPU proyectada con %d instancias (%.1f%%) alcanzaria u_high (%.0f%%)",
			target, projected, p.UHigh)
	}
	if lat := obs.get(MetricLatencyP90); lat != nil && len(lat.Samples) > 0 && lat.Value >= p.ReduceMaxP90Seconds {
		return false, fmt.Sprintf("la latencia p90 (%.3fs) supera el limite para reducir (%.3fs)",
			lat.Value, p.ReduceMaxP90Seconds)
	}
	return true, ""
}

// rpsSlope calcula la pendiente de una serie temporal usando minimos cuadrados.
//  cuanto crece RequestCountPerTarget por cada periodo de AggregationPeriodSeconds.
// Con menos de 2 puntos devuelve 0.
func rpsSlope(samples []float64) float64 {
	n := len(samples)
	if n < 2 {
		return 0
	}
	// minimos cuadrados: pendiente = (n*Sxy - Sx*Sy) / (n*Sxx - Sx^2)
	var sx, sy, sxy, sxx float64
	for i, y := range samples {
		x := float64(i)
		sx += x
		sy += y
		sxy += x * y
		sxx += x * x
	}
	fn := float64(n)
	denom := fn*sxx - sx*sx
	if math.Abs(denom) < 1e-12 {
		return 0
	}
	return (fn*sxy - sx*sy) / denom
}

// Funciones auxiliares matematicas

// devuelve los ultimos n elementos de v (o todos si hay menos de n)
func lastN(v []float64, n int) []float64 {
	if len(v) <= n {
		return v
	}
	return v[len(v)-n:]
}

// cuenta cuantos elementos de v cumplen la condicion f
func countIf(v []float64, f func(float64) bool) int {
	n := 0
	for _, x := range v {
		if f(x) {
			n++
		}
	}
	return n
}

// calcula la media aritmetica de v (devuelve 0 si v esta vacio)
func mean(v []float64) float64 {
	if len(v) == 0 {
		return 0
	}
	s := 0.0
	for _, x := range v {
		s += x
	}
	return s / float64(len(v))
}