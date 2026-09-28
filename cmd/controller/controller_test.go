package main

import (
	"context"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Momento fijo para todos los tests (reproducible).
var testNow = time.Date(2026, 9, 20, 12, 0, 0, 0, time.UTC)

// testParams devuelve los parametros del experimento real (los del config.yaml).
func testParams() Params {
	return Params{
		NSamples: 4, UHigh: 70, ULow: 30, KUp: 3, KDown: 4,
		CooldownSeconds: 300, Step: 1, MinCapacity: 1, MaxCapacity: 5,
		ReduceMaxP90Seconds: 0.6, MetricFreshnessSeconds: 180,
		ObservationWindowSeconds: 360, AggregationPeriodSeconds: 60,
		ExpectedPublishLagSeconds: 90,
		ErrorRateScaleUpPct: 5.0,
		RPSSlopeThreshold:   0, // desactivado por defecto en los tests base
	}
}

// testObs construye una Observation con los valores dados
func testObs(cpu []float64, desired, healthy, pending int, lat float64) *Observation {
	o := &Observation{
		Metrics: map[string]*Metric{
			MetricCPU: {Samples: cpu, Value: mean(cpu), LatestAt: testNow},
		},
		Capacity: Capacity{Desired: desired, InService: desired - pending, Pending: pending, HealthyTargets: healthy},
	}
	if lat >= 0 {
		o.Metrics[MetricLatencyP90] = &Metric{Samples: []float64{lat}, Value: lat, LatestAt: testNow}
	}
	return o
}

// testDecide es un helper que llama a decide() con los parametros del taller.
func testDecide(o *Observation, last time.Time, q Quality) Outcome {
	return decide(o, q, last, testNow, testParams())
}

// expectDecision verifica la decision y el codigo de razon, y que exista razon en texto
func expectDecision(t *testing.T, out Outcome, d Decision, code string) {
	t.Helper()
	if out.Decision != d || out.ReasonCode != code {
		t.Fatalf("esperaba %s/%s, obtuve %s/%s (%s)", d, code, out.Decision, out.ReasonCode, out.Reason)
	}
	if out.Reason == "" {
		t.Fatal("toda decision, incluido MAINTAIN, debe llevar razon en texto")
	}
}

// ============================================================
// Tests de la politica de decision
// ============================================================

// CPU alta sostenida (3 de 4 muestras > 70) → debe subir.
func TestSubeConPresionSostenida(t *testing.T) {
	out := testDecide(testObs([]float64{75, 78, 82, 80}, 2, 2, 0, 0.3), time.Time{}, DataOK)
	expectDecision(t, out, IncreaseCapacity, ReasonSustainedHigh)
	if out.Target != 3 {
		t.Fatalf("esperaba objetivo 3, obtuve %d", out.Target)
	}
}

// Un pico de 2 min solo llena 2 muestras de 60 s: k_up=3 no se cumple MAINTAIN.
func TestNoSubeConUnTransitorio(t *testing.T) {
	out := testDecide(testObs([]float64{20, 22, 95, 96}, 2, 2, 0, 0.2), time.Time{}, DataOK)
	expectDecision(t, out, MaintainCapacity, ReasonWithinBand)
}

// Bajar exige mas evidencia que subir: 3 de 4 muestras bajas no alcanzan (k_down=4).
func TestBajarExigeMasEvidenciaQueSubir(t *testing.T) {
	out := testDecide(testObs([]float64{10, 10, 10, 50}, 3, 3, 0, 0.1), time.Time{}, DataOK)
	expectDecision(t, out, MaintainCapacity, ReasonWithinBand)
}

// Datos marcados como insuficientes nunca deben mover capacidad.
func TestDatosInsuficientesNuncaMuevenCapacidad(t *testing.T) {
	out := testDecide(testObs([]float64{5, 5, 5, 5}, 4, 4, 0, 0.1), time.Time{}, DataInsufficient)
	expectDecision(t, out, MaintainCapacity, ReasonDataInsufficient)
}

// Si no hay metrica de CPU en absoluto, no debe entrar en panico.
func TestSinMetricaDeCPUNoPanicaYMantiene(t *testing.T) {
	o := testObs(nil, 2, 2, 0, -1)
	delete(o.Metrics, MetricCPU)
	out := testDecide(o, time.Time{}, DataOK)
	expectDecision(t, out, MaintainCapacity, ReasonDataInsufficient)
}

// Con una instancia en Pending - MAINTAIN (esperando que termine de lanzar).
func TestNoDecideConCapacidadEnTransicion(t *testing.T) {
	high := []float64{90, 92, 95, 96}
	expectDecision(t, testDecide(testObs(high, 3, 2, 1, 2.0), time.Time{}, DataOK),
		MaintainCapacity, ReasonCapacityUnstable)
}

// Menos targets sanos que deseados, SIN pending/terminating (instancias ya
// establecidas que fallan el health check, p.ej. por sobrecarga) - INCREASE.
func TestInstanciaEstablecidaNoSanaFuerzaSubida(t *testing.T) {
	high := []float64{90, 92, 95, 96}
	out := testDecide(testObs(high, 3, 2, 0, 2.0), time.Time{}, DataOK)
	expectDecision(t, out, IncreaseCapacity, ReasonUnhealthyEstablished)
	if out.Target != 4 {
		t.Fatalf("esperaba target=4, obtuve %d", out.Target)
	}
}

// Igual que arriba, pero ya en el maximo - MAINTAIN (no hay a donde subir).
func TestInstanciaEstablecidaNoSanaEnElMaximoMantiene(t *testing.T) {
	high := []float64{90, 92, 95, 96}
	out := testDecide(testObs(high, 5, 4, 0, 2.0), time.Time{}, DataOK)
	expectDecision(t, out, MaintainCapacity, ReasonSaturatedAtMax)
}

// Ya en max y con CPU alta - MAINTAIN (no se puede subir mas).
func TestNoSuperaElMaximo(t *testing.T) {
	out := testDecide(testObs([]float64{95, 97, 99, 98}, 5, 5, 0, 3.0), time.Time{}, DataOK)
	expectDecision(t, out, MaintainCapacity, ReasonSaturatedAtMax)
}

// Ya en min y con CPU baja - MAINTAIN (no se puede bajar mas).
func TestNoBajaDelMinimo(t *testing.T) {
	out := testDecide(testObs([]float64{2, 2, 2, 2}, 1, 1, 0, 0.1), time.Time{}, DataOK)
	expectDecision(t, out, MaintainCapacity, ReasonAtMin)
}

// El cooldown bloquea tanto subir como bajar.
func TestCooldownBloqueaSubidaYBajada(t *testing.T) {
	recent := testNow.Add(-60 * time.Second) // 60 s < 300 s de cooldown
	expectDecision(t, testDecide(testObs([]float64{80, 85, 88, 90}, 2, 2, 0, 0.3), recent, DataOK),
		MaintainCapacity, ReasonCooldown)
	expectDecision(t, testDecide(testObs([]float64{10, 11, 12, 10}, 3, 3, 0, 0.1), recent, DataOK),
		MaintainCapacity, ReasonCooldown)
}

// Una vez que pasa el cooldown (>300 s) vuelve a decidir normalmente.
func TestTerminadoElCooldownVuelveADecidir(t *testing.T) {
	old := testNow.Add(-301 * time.Second)
	out := testDecide(testObs([]float64{80, 85, 88, 90}, 2, 2, 0, 0.3), old, DataOK)
	expectDecision(t, out, IncreaseCapacity, ReasonSustainedHigh)
}

// CPU baja sostenida y latencia OK - debe bajar.
func TestReduceCuandoEsSeguro(t *testing.T) {
	out := testDecide(testObs([]float64{10, 11, 12, 10}, 3, 3, 0, 0.1), time.Time{}, DataOK)
	expectDecision(t, out, ReduceCapacity, ReasonSustainedLow)
	if out.Target != 2 {
		t.Fatalf("esperaba objetivo 2, obtuve %d", out.Target)
	}
}

// Sin trafico el ALB no publica latencia: no debe impedir reducir.
func TestReduceSinMetricaDeLatencia(t *testing.T) {
	out := testDecide(testObs([]float64{5, 5, 5, 5}, 2, 2, 0, -1), time.Time{}, DataOK)
	expectDecision(t, out, ReduceCapacity, ReasonSustainedLow)
}

// Latencia p90 alta bloquea la reduccion aunque la CPU sea baja.
func TestLatenciaAltaBloqueaLaReduccion(t *testing.T) {
	out := testDecide(testObs([]float64{10, 11, 12, 10}, 3, 3, 0, 0.7), time.Time{}, DataOK)
	expectDecision(t, out, MaintainCapacity, ReasonUnsafeToReduce)
}

// La proyeccion de CPU bloquea si la banda esta mal configurada.
func TestLaProyeccionBloqueaSiLaBandaEsIncoherente(t *testing.T) {
	p := testParams()
	p.ULow, p.UHigh = 60, 70                            // banda demasiado estrecha
	o := testObs([]float64{58, 58, 58, 58}, 3, 3, 0, 0.1) // CPU proyectada con 2: 87%
	out := decide(o, DataOK, time.Time{}, testNow, p)
	expectDecision(t, out, MaintainCapacity, ReasonUnsafeToReduce)
}

// CPU en la banda (30%-70%) y sin evidencia sostenida - MAINTAIN.
func TestDentroDeLaBandaMantiene(t *testing.T) {
	out := testDecide(testObs([]float64{50, 52, 51, 49}, 2, 2, 0, 0.2), time.Time{}, DataOK)
	expectDecision(t, out, MaintainCapacity, ReasonWithinBand)
}

// ============================================================
// Tests de HIGH_ERROR_RATE con la correccion de HealthyTargets
// ============================================================

// crea una Observation con CPU baja y errores 5xx configurables
func buildObsWithErrors(desired, healthy int, elbErrs, tgtErrs, rpt float64) *Observation {
	o := testObs([]float64{10, 10, 10, 10}, desired, healthy, 0, 0.1)
	o.Metrics[MetricELB5xx] = &Metric{
		Samples: []float64{elbErrs}, Value: elbErrs, LatestAt: testNow,
	}
	o.Metrics[MetricTarget5xx] = &Metric{
		Samples: []float64{tgtErrs}, Value: tgtErrs, LatestAt: testNow,
	}
	o.Metrics[MetricRPT] = &Metric{
		Samples: []float64{rpt}, Value: rpt, LatestAt: testNow,
	}
	return o
}

// Tasa de errores ELB sola supera el umbral - INCREASE.
func TestHighErrorRateELBSoloDispara(t *testing.T) {
	// desired=2, healthy=2, rpt=100 req/target → total=200
	// elbErrs=20 → errorRate = 20/200 = 10% > 5%
	o := buildObsWithErrors(2, 2, 20, 0, 100)
	out := decide(o, DataOK, time.Time{}, testNow, testParams())
	expectDecision(t, out, IncreaseCapacity, ReasonHighErrorRate)
}

// Tasa de errores Target sola supera el umbral - INCREASE.
func TestHighErrorRateTargetSoloDispara(t *testing.T) {
	o := buildObsWithErrors(2, 2, 0, 20, 100)
	out := decide(o, DataOK, time.Time{}, testNow, testParams())
	expectDecision(t, out, IncreaseCapacity, ReasonHighErrorRate)
}

// Ambas fuentes sumadas superan el umbral (individualmente no lo superarian).
func TestHighErrorRateAmbasSumadas(t *testing.T) {
	// ELB: 6 errores (3%) + Target: 6 errores (3%) = 6% total > 5%
	o := buildObsWithErrors(2, 2, 6, 6, 100)
	out := decide(o, DataOK, time.Time{}, testNow, testParams())
	expectDecision(t, out, IncreaseCapacity, ReasonHighErrorRate)
}


// Tasa de errores baja no dispara escalado.
func TestBajaErrorRateNoDispara(t *testing.T) {
	// 4 errores / 200 peticiones = 2% < 5%
	o := buildObsWithErrors(2, 2, 2, 2, 100)
	out := decide(o, DataOK, time.Time{}, testNow, testParams())
	// debe decidir por CPU baja (REDUCE) o WITHIN_BAND, no HIGH_ERROR_RATE
	if out.ReasonCode == ReasonHighErrorRate {
		t.Fatalf("2%% de errores no debe disparar HIGH_ERROR_RATE, obtuve %s", out.Reason)
	}
}

// ============================================================
// Tests de PROACTIVE_RPS_TREND
// ============================================================

func testObsWithRPT(cpu []float64, desired, healthy int, rptSamples []float64) *Observation {
	o := testObs(cpu, desired, healthy, 0, 0.1)
	o.Metrics[MetricRPT] = &Metric{
		Samples: rptSamples,
		Value:   mean(rptSamples),
		LatestAt: testNow,
	}
	return o
}

// RPS creciendo rapido (pendiente > umbral) - INCREASE con PROACTIVE_RPS_TREND.
func TestProactivoDisparaConPendienteAlta(t *testing.T) {
	p := testParams()
	p.RPSSlopeThreshold = 5.0 // 5 req/target/periodo
	// RPS por target: 10, 20, 30, 40 → pendiente = +10/periodo
	o := testObsWithRPT([]float64{15, 15, 15, 15}, 2, 2, []float64{10, 20, 30, 40})
	out := decide(o, DataOK, time.Time{}, testNow, p)
	expectDecision(t, out, IncreaseCapacity, ReasonProactiveRPSTrend)
}

// RPS plano - no dispara escalado proactivo.
func TestProactivoNoDisparaConRPSPlano(t *testing.T) {
	p := testParams()
	p.RPSSlopeThreshold = 5.0
	// RPS constante: pendiente ≈ 0
	o := testObsWithRPT([]float64{15, 15, 15, 15}, 2, 2, []float64{50, 50, 50, 50})
	out := decide(o, DataOK, time.Time{}, testNow, p)
	if out.ReasonCode == ReasonProactiveRPSTrend {
		t.Fatalf("RPS plano no debe disparar PROACTIVE_RPS_TREND, obtuve %s", out.Reason)
	}
}

// RPSSlopeThreshold = 0 desactiva completamente la guarda proactiva.
func TestProactivoDesactivadoCuandoUmbralEsCero(t *testing.T) {
	p := testParams()
	p.RPSSlopeThreshold = 0 // desactivado
	// RPS creciendo muy rapido
	o := testObsWithRPT([]float64{15, 15, 15, 15}, 2, 2, []float64{10, 100, 200, 500})
	out := decide(o, DataOK, time.Time{}, testNow, p)
	if out.ReasonCode == ReasonProactiveRPSTrend {
		t.Fatalf("umbral=0 debe desactivar PROACTIVE, obtuve %s", out.Reason)
	}
}

// Ya en el maximo: no puede subir aunque la pendiente sea alta.
func TestProactivoEnElMaximoMantiene(t *testing.T) {
	p := testParams()
	p.RPSSlopeThreshold = 5.0
	o := testObsWithRPT([]float64{15, 15, 15, 15}, 5, 5, []float64{10, 20, 30, 40})
	out := decide(o, DataOK, time.Time{}, testNow, p)
	expectDecision(t, out, MaintainCapacity, ReasonSaturatedAtMax)
}

// ============================================================
// Tests de rpsSlope
// ============================================================

func TestRPSSlopeCreciente(t *testing.T) {
	slope := rpsSlope([]float64{10, 20, 30, 40})
	if math.Abs(slope-10) > 0.01 {
		t.Fatalf("pendiente esperada 10, obtuve %.4f", slope)
	}
}

func TestRPSSlopePlano(t *testing.T) {
	slope := rpsSlope([]float64{50, 50, 50, 50})
	if math.Abs(slope) > 0.01 {
		t.Fatalf("pendiente esperada 0, obtuve %.4f", slope)
	}
}

func TestRPSSlopeConMenosDe2Puntos(t *testing.T) {
	if rpsSlope([]float64{}) != 0 {
		t.Fatal("slice vacio debe devolver 0")
	}
	if rpsSlope([]float64{42}) != 0 {
		t.Fatal("un solo punto debe devolver 0")
	}
}

// ============================================================
// Tests de persistencia de estado (loadState / saveState)
// ============================================================

func TestLoadStateSinArchivoDaEstadoCero(t *testing.T) {
	// Resetear variables globales para test aislado
	lastActionAt = time.Time{}
	cycle = 0
	loadState(filepath.Join(t.TempDir(), "nonexistent.json"))
	if !lastActionAt.IsZero() {
		t.Fatal("archivo inexistente debe dejar lastActionAt como zero")
	}
	if cycle != 0 {
		t.Fatal("archivo inexistente debe dejar cycle como 0")
	}
}

func TestLoadStateArchivoCorruptoNoPanica(t *testing.T) {
	lastActionAt = time.Time{}
	cycle = 0
	f, _ := os.CreateTemp(t.TempDir(), "state*.json")
	f.WriteString("esto no es json valido")
	f.Close()
	// no debe hacer panic
	loadState(f.Name())
	if !lastActionAt.IsZero() {
		t.Fatal("estado corrupto debe dejar lastActionAt como zero")
	}
}

func TestSaveStateYLoadStateRoundtrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")
	at := time.Date(2026, 9, 20, 12, 0, 0, 0, time.UTC)

	saveState(path, at, 42)

	// Resetear y cargar
	lastActionAt = time.Time{}
	cycle = 0
	loadState(path)

	if !lastActionAt.Equal(at) {
		t.Fatalf("lastActionAt esperado %v, obtuve %v", at, lastActionAt)
	}
	if cycle != 42 {
		t.Fatalf("cycle esperado 42, obtuve %d", cycle)
	}
}

func TestSaveStateEsAtomico(t *testing.T) {
	// Verificar que no queda archivo temporal si saveState termina correctamente
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")
	saveState(path, testNow, 1)

	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".tmp") {
			t.Fatalf("no debe quedar archivo temporal despues de saveState: %s", e.Name())
		}
	}
}

// ============================================================
// Tests de configuracion (Validate)
// ============================================================

func validConfig() *Config {
	return &Config{Params: Params{
		LoopIntervalSeconds: 30, AggregationPeriodSeconds: 60, NSamples: 4,
		ObservationWindowSeconds: 360, ExpectedPublishLagSeconds: 90,
		UHigh: 70, ULow: 30, KUp: 3, KDown: 4, CooldownSeconds: 300,
		Step: 1, MinCapacity: 1, MaxCapacity: 5,
		RPSSlopeThreshold: 0,
	}}
}

// El archivo config.yaml que se despliega debe pasar todas las reglas.
func TestLaConfiguracionVigenteEsValida(t *testing.T) {
	if _, err := loadConfig("../../config/config.yaml"); err != nil {
		t.Fatal(err)
	}
}

// min < 1 o max > 5 deben rechazarse: son restricciones del taller.
func TestRechazaLimitesFueraDelTaller(t *testing.T) {
	c := validConfig()
	c.Params.MaxCapacity = 6
	if err := c.Validate(); err == nil {
		t.Fatal("max_capacity=6 debe rechazarse")
	}
	c = validConfig()
	c.Params.MinCapacity = 0
	if err := c.Validate(); err == nil {
		t.Fatal("min_capacity=0 debe rechazarse")
	}
}

// Si u_low esta muy cerca de u_high, un scale-up dispara un scale-down inmediato.
func TestRechazaBandaMuertaIncoherente(t *testing.T) {
	c := validConfig()
	c.Params.ULow = 40 // con min=1, step=1: u_high*1/2 = 35 → u_low debe ser < 35
	err := c.Validate()
	if err == nil || !strings.Contains(err.Error(), "banda muerta") {
		t.Fatalf("esperaba error de banda muerta, obtuve %v", err)
	}
}

// Si la ventana no cubre el retardo de CloudWatch, k_down puede ser inalcanzable.
func TestRechazaVentanaQueNoCubreElRetardoDeCloudWatch(t *testing.T) {
	c := validConfig()
	c.Params.ObservationWindowSeconds = 240 // necesita 4*60+90=330 s
	err := c.Validate()
	if err == nil || !strings.Contains(err.Error(), "observation_window") {
		t.Fatalf("esperaba error de ventana, obtuve %v", err)
	}
}

// k_down <= k_up significaria que bajar exige igual o menos evidencia que subir.
func TestBajarDebeExigirMasEvidenciaQueSubir(t *testing.T) {
	c := validConfig()
	c.Params.KDown = c.Params.KUp
	if err := c.Validate(); err == nil {
		t.Fatal("k_down <= k_up debe rechazarse")
	}
}

// rps_slope_threshold negativo debe rechazarse.
func TestRechazaSlopeThresholdNegativo(t *testing.T) {
	c := validConfig()
	c.Params.RPSSlopeThreshold = -1.0
	if err := c.Validate(); err == nil {
		t.Fatal("rps_slope_threshold < 0 debe rechazarse")
	}
}

// rps_slope_threshold = 0 (desactivado) debe aceptarse.
func TestAceptaSlopeThresholdCero(t *testing.T) {
	c := validConfig()
	c.Params.RPSSlopeThreshold = 0
	if err := c.Validate(); err != nil {
		t.Fatalf("rps_slope_threshold=0 debe ser valido, obtuve: %v", err)
	}
}

// ============================================================
// Tests de validacion de metricas (validateMetrics)
// ============================================================

// Construye una Observation con CPU y la edad dada.
func makeObs(age time.Duration, samples ...float64) *Observation {
	return &Observation{
		Metrics: map[string]*Metric{
			MetricCPU: {Samples: append([]float64(nil), samples...), LatestAt: testNow.Add(-age)},
		},
		Capacity: Capacity{Desired: 2, HealthyTargets: 2},
	}
}

// Datos buenos y frescos → OK.
func TestDatosBuenosSonOK(t *testing.T) {
	obs := makeObs(100*time.Second, 50, 51, 52, 53)
	if q := validateMetrics(obs, testParams(), testNow); q != DataOK {
		t.Fatalf("esperaba OK, obtuve %s", q)
	}
}

// NaN, >100 y negativo deben descartarse; las 4 muestras validas alcanzan.
func TestDescartaDatapointsImposibles(t *testing.T) {
	obs := makeObs(100*time.Second, 50, math.NaN(), 250, -3, 51, 52, 53)
	q := validateMetrics(obs, testParams(), testNow)
	if len(obs.Discarded) != 3 {
		t.Fatalf("esperaba 3 descartados (NaN, >100, negativo), obtuve %v", obs.Discarded)
	}
	if q != DataOK {
		t.Fatalf("las 4 muestras validas alcanzan: calidad=%s", q)
	}
}

// 2 muestras < k_up=3 → insuficiente.
func TestPocasMuestrasEsInsuficiente(t *testing.T) {
	obs := makeObs(100*time.Second, 50, 51)
	if q := validateMetrics(obs, testParams(), testNow); q != DataInsufficient {
		t.Fatalf("2 muestras < k_up=3 deben ser insuficientes, obtuve %s", q)
	}
}

// Dato de hace 10 min supera metric_freshness_seconds=180 s → insuficiente.
func TestDatoViejoEsInsuficiente(t *testing.T) {
	obs := makeObs(10*time.Minute, 50, 51, 52, 53)
	if q := validateMetrics(obs, testParams(), testNow); q != DataInsufficient {
		t.Fatalf("dato de hace 10 min no sirve para decidir, obtuve %s", q)
	}
}

// Sin metrica de CPU -> insuficiente.
func TestSinCPUEsInsuficiente(t *testing.T) {
	obs := &Observation{Metrics: map[string]*Metric{}, Capacity: Capacity{Desired: 2, HealthyTargets: 2}}
	if q := validateMetrics(obs, testParams(), testNow); q != DataInsufficient {
		t.Fatalf("sin CPU no hay base para decidir, obtuve %s", q)
	}
}

// ============================================================
// Tests de Leader Election (Simulacion de Lider / Follower)
// ============================================================

type mockElector struct {
	isLeader bool
}

func (m *mockElector) TryAcquireOrRenew(ctx context.Context) (bool, error) {
	return m.isLeader, nil
}

func TestSimulacionLiderYFollower(t *testing.T) {
	// Instancia A (Lider)
	electorA := &mockElector{isLeader: true}
	leaderA, errA := electorA.TryAcquireOrRenew(context.Background())
	if errA != nil || !leaderA {
		t.Fatalf("Instancia A deberia ser el Lider activo")
	}

	// Instancia B (Follower en Standby)
	electorB := &mockElector{isLeader: false}
	leaderB, errB := electorB.TryAcquireOrRenew(context.Background())
	if errB != nil || leaderB {
		t.Fatalf("Instancia B deberia ser Follower en Standby")
	}

	// Simular Failover: El Lider A cae, B toma el mando
	electorB.isLeader = true
	leaderBAfterFailover, _ := electorB.TryAcquireOrRenew(context.Background())
	if !leaderBAfterFailover {
		t.Fatalf("Instancia B deberia tomar el liderazgo tras la caida de A")
	}
}
