// entry point, punto de control, logging

// Flags:
//	-dry-run  observa, decide y registra,no modifica la infra
//	-once     ejecuta un solo ciclo y termina 

package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"
)

var (
	// guarda el tiempo de la última acción (se pierde al reinicia)
	lastActionAt time.Time
	logFile *os.File // archivo de decisiones JSONL (append)
	cycle   int64    // contador de ciclos, empieza en 0
)

func main() {
	cfgPath    := flag.String("config", "/etc/autoscaling-controller/config.yaml", "ruta de la configuracion")
	experiment := flag.String("experiment", "", "id del experimento (sobrescribe el del archivo)")
	dry        := flag.Bool("dry-run", false, "observa y decide pero no actua sobre la infraestructura")
	once       := flag.Bool("once", false, "ejecutar un solo ciclo y salir")
	flag.Parse()

	cfg, err := loadConfig(*cfgPath)
	if err != nil {
		log.Fatalf("configuracion invalida: %v", err)
	}
	if *experiment != "" {
		cfg.ExperimentID = *experiment
	}

	// Abrir el archivo de log de decisiones en modo append
	if err := os.MkdirAll(filepath.Dir(cfg.DecisionLog), 0o755); err != nil {
		log.Fatalf("creando directorio de log: %v", err)
	}
	logFile, err = os.OpenFile(cfg.DecisionLog, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		log.Fatalf("abriendo log de decisiones: %v", err)
	}
	defer logFile.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	clients, err := newClients(ctx, cfg)
	if err != nil {
		log.Fatalf("inicializando clientes de AWS: %v", err)
	}

	p := cfg.Params
	fmt.Printf("controlador iniciado  experimento=%s  intervalo=%ds  capacidad=[%d,%d]\n",
		cfg.ExperimentID, p.LoopIntervalSeconds, p.MinCapacity, p.MaxCapacity)
	if *dry {
		fmt.Println("MODO DRY-RUN: se observa, decide y registra sin modificar la infraestructura")
	}

	run := func() {
		cctx, ccancel := context.WithTimeout(ctx, p.LoopInterval())
		defer ccancel()
		r := runCycle(cctx, cfg, clients, *dry)
		fmt.Printf("%s  ciclo=%d  %s  capacidad=%d  calidad=%s  resultado=%s  %s\n",
			r.Timestamp.Format(time.RFC3339), r.Cycle, r.Decision,
			r.Capacity.Desired, r.Quality, r.Result.Status, r.ReasonCode)
	}

	if *once {
		run()
		return
	}

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	ticker := time.NewTicker(p.LoopInterval())
	defer ticker.Stop()

	run() // primer ciclo inmediato
	for {
		select {
		case <-ticker.C:
			run()
		case <-sig:
			// al terminar, la capacidad queda congelada en su ultimo valor modo fail-safe
			fmt.Println("señal recibida: el controlador termina, la capacidad queda congelada")
			return
		}
	}
}

// ejecuta un ciclo completo observar - decidir -registrar - actuar
func runCycle(ctx context.Context, cfg *Config, clients *Clients, dry bool) *Record {
	now := time.Now().UTC()
	cycle++
	p := cfg.Params

	obs, quality, err := observe(ctx, clients, cfg, now)

	// armar el registro con lo que tenemos (la observacion puede venir parcial si hubo error)
	rec := &Record{
		Cycle:          cycle,
		ExperimentID:   cfg.ExperimentID,
		Params:         p,
		Timestamp:      now,
		Quality:        quality,
		Metrics:        map[string]*Metric{},
		Discarded:      []string{},
		CooldownActive: cooldownActive(lastActionAt, now, p),
	}
	if !lastActionAt.IsZero() {
		rec.SecondsSinceLastAction = now.Sub(lastActionAt).Seconds()
	}
	if obs != nil {
		rec.Window = obs.Window
		rec.Capacity = obs.Capacity
		rec.Metrics = obs.Metrics
		if obs.Discarded != nil {
			rec.Discarded = obs.Discarded
		}
	}

	// Si fallo la observacion, congelar es lo mas seguro
	if err != nil {
		rec.Decision = MaintainCapacity
		rec.ReasonCode = ReasonDataInsufficient
		rec.Reason = fmt.Sprintf("Fallo al observar (%v): se mantiene la capacidad.", err)
		rec.Result = Result{Status: "NO_ACTION"}
		writeLog(rec)
		return rec
	}

	out := decide(obs, quality, lastActionAt, now, p)
	rec.Decision = out.Decision
	rec.ReasonCode = out.ReasonCode
	rec.Reason = out.Reason

	if out.Decision == MaintainCapacity {
		rec.Result = Result{Status: "NO_ACTION"}
		writeLog(rec)
		return rec
	}

	rec.Action = &Action{From: obs.Capacity.Desired, To: out.Target}

	if dry {
		fmt.Printf("[dry-run] se habria pedido SetDesiredCapacity=%d\n", out.Target)
		rec.Result = Result{Status: "DRY_RUN"}
		writeLog(rec)
		return rec
	}

	start := time.Now()
	actErr := setDesiredCapacity(ctx, clients, cfg, out.Target)
	rec.Result.LatencyMS = time.Since(start).Milliseconds()

	if actErr != nil {
		// La accion fallo: no se actualiza lastActionAt, asi que el siguiente
		// ciclo (en 30 s) reevaluara con datos frescos sin cooldown ficticio.
		rec.Result.Status = "FAILED"
		rec.Result.Error = actErr.Error()
	} else {
		rec.Result.Status = "SUCCEEDED"
		lastActionAt = time.Now().UTC()
	}
	writeLog(rec)
	return rec
}

// escribe un registro en el archivo JSONL 
func writeLog(rec *Record) {
	if err := json.NewEncoder(logFile).Encode(rec); err != nil {
		log.Printf("no se pudo registrar el ciclo %d: %v", rec.Cycle, err)
	}
}
