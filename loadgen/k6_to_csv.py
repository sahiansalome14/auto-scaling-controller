#!/usr/bin/env python3
"""k6_to_csv.py — Convierte la salida CSV bruta de k6 al formato columnar
del pipeline de experimentos SI3016.

Uso:
    python3 loadgen/k6_to_csv.py \\
        --k6csv   /tmp/k6_raw.csv \\
        --profile loadgen/profiles/E2.json \\
        --experiment E2-run01 \\
        --out results/E2/run01/loadgen.csv

Formato de entrada (k6 --out csv):
    metric_name,timestamp,metric_value,...,status,...

Formato de salida:
    experiment_id,t_seconds,offered,served,ok,errors,
    latency_p50_s,latency_p90_s,latency_max_s
"""

import argparse
import csv
import json
import os
import sys
from collections import defaultdict


def rate_at(profile, t):
    """Tasa objetivo (req/s) en el segundo t."""
    pts = profile["points"]
    if t <= pts[0]["t"]:
        return pts[0]["rps"]
    for a, b in zip(pts, pts[1:]):
        if a["t"] <= t <= b["t"]:
            if b.get("step") or b["t"] == a["t"]:
                return b["rps"] if t == b["t"] else a["rps"]
            f = (t - a["t"]) / (b["t"] - a["t"])
            return a["rps"] + (b["rps"] - a["rps"]) * f
    return pts[-1]["rps"]


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--k6csv",      required=True, help="CSV bruto de k6")
    ap.add_argument("--profile",    required=True, help="Perfil JSON del experimento")
    ap.add_argument("--experiment", required=True, help="ID del experimento (ej. E2-run01)")
    ap.add_argument("--out",        required=True, help="Ruta del CSV de salida")
    ap.add_argument("--timeout",    type=float, default=5.0)
    args = ap.parse_args()

    with open(args.profile) as f:
        profile = json.load(f)
    duration = profile["duration_seconds"]

    # Acumular datos por segundo a partir del CSV crudo de k6.
    # k6 escribe una fila por muestra; nos interesan las filas de http_req_duration
    # (una por petición HTTP completada).
    per_second = defaultdict(lambda: {"lat": [], "ok": 0, "err": 0, "served": 0})
    start_ts = None  # timestamp Unix (s) de la primera petición observada

    with open(args.k6csv, newline="", encoding="utf-8") as f:
        reader = csv.DictReader(f)
        for row in reader:
            if row.get("metric_name") != "http_req_duration":
                continue

            # k6 --out csv escribe el timestamp en SEGUNDOS desde Unix epoch
            # (no en ms). Dividir por 1000 metia todas las filas en t=0.
            try:
                ts_s = float(row["timestamp"])
            except (KeyError, ValueError):
                continue

            if start_ts is None:
                start_ts = ts_s

            sec = int(ts_s - start_ts)
            if sec < 0 or sec >= duration:
                continue

            # Latencia en ms → s, acotada al timeout para evitar artefactos.
            try:
                lat_s = min(float(row["metric_value"]) / 1000.0, args.timeout)
            except (KeyError, ValueError):
                lat_s = args.timeout

            # Estado HTTP (tag de k6).
            try:
                status = int(row.get("status") or 0)
            except ValueError:
                status = 0

            # k6 registra duracion 0 en las peticiones que expiran (status 0):
            # las contamos con el valor del timeout para no falsear p50/p90.
            if status == 0:
                lat_s = args.timeout

            b = per_second[sec]
            b["served"] += 1
            b["lat"].append(lat_s)
            if 200 <= status < 400:
                b["ok"] += 1
            else:
                b["err"] += 1

    if start_ts is None:
        print("AVISO: no se encontraron filas http_req_duration en el CSV de k6.",
              file=sys.stderr)

    os.makedirs(os.path.dirname(args.out) or ".", exist_ok=True)
    with open(args.out, "w", newline="") as f:
        w = csv.writer(f)
        w.writerow(["experiment_id", "t_seconds", "offered", "served", "ok", "errors",
                    "latency_p50_s", "latency_p90_s", "latency_max_s"])
        for sec in range(duration):
            b = per_second[sec]
            lat = sorted(b["lat"])
            offered = round(rate_at(profile, sec))
            p50 = lat[len(lat) // 2]          if lat else 0
            p90 = lat[int(len(lat) * 0.9)]    if lat else 0
            lmax = max(lat)                    if lat else 0
            w.writerow([args.experiment, sec,
                        offered, b["served"], b["ok"], b["err"],
                        f"{p50:.4f}", f"{p90:.4f}", f"{lmax:.4f}"])

    print(f"resultados en {args.out}  "
          f"({sum(b['served'] for b in per_second.values())} peticiones procesadas)")


if __name__ == "__main__":
    main()