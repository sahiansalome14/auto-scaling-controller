"""
plot_results.py — genera una figura con 4 subplots para un directorio de resultados.

Uso:
    python scripts/plot_results.py results/E2/run01 [results/E4/run01 ...]
"""
import os
import sys
import json
import math
import pandas as pd
import matplotlib
matplotlib.use("Agg")
import matplotlib.pyplot as plt
import matplotlib.patches as mpatches
from matplotlib.lines import Line2D

# ---------------------------------------------------------------------------
# Lectura de datos
# ---------------------------------------------------------------------------

def load_decisions(jsonl_path):
    """Carga decisions.jsonl y devuelve una lista de dicts."""
    records = []
    with open(jsonl_path, "r", encoding="utf-8") as f:
        for line in f:
            line = line.strip()
            if not line:
                continue
            try:
                records.append(json.loads(line))
            except json.JSONDecodeError:
                continue
    return records


def records_to_df(records):
    """Convierte la lista de records a un DataFrame con las columnas que necesitamos."""
    rows = []
    for d in records:
        ts = pd.to_datetime(d.get("timestamp"), utc=True, errors="coerce")
        m = d.get("metrics", {})

        def val(key):
            entry = m.get(key)
            if entry is None:
                return float("nan")
            return entry.get("value", float("nan"))

        rows.append({
            "timestamp":      ts,
            "cycle":          d.get("cycle", 0),
            "desired":        d.get("capacity", {}).get("desired", float("nan")),
            "healthy":        d.get("capacity", {}).get("healthy_targets", float("nan")),
            "pending":        d.get("capacity", {}).get("pending", 0),
            "decision":       d.get("decision", ""),
            "reason_code":    d.get("reason_code", ""),
            "action_from":    (d.get("requested_action") or {}).get("from", float("nan")),
            "action_to":      (d.get("requested_action") or {}).get("to",   float("nan")),
            "result_status":  d.get("result", {}).get("status", ""),
            "cpu_avg":        val("cpu_utilization"),
            "cpu_max":        val("cpu_utilization_max"),
            "lat_p90":        val("target_response_time_p90"),
            "rpt":            val("request_count_per_target"),
            "e5x_elb":        val("http_5xx_elb_count"),
            "e5x_tgt":        val("http_5xx_target_count"),
            "conn_active":    val("active_connection_count"),
            "conn_err":       val("target_connection_error_count"),
        })
    df = pd.DataFrame(rows)
    if df.empty:
        return df
    df = df.sort_values("timestamp").reset_index(drop=True)
    # t_seconds relativo al primer ciclo
    t0 = df["timestamp"].min()
    df["t_seconds"] = (df["timestamp"] - t0).dt.total_seconds()
    return df


def load_params(records):
    """Extrae u_high y u_low del primer record que los tenga."""
    for d in records:
        p = d.get("params", {})
        if isinstance(p, dict) and "u_high" in p:
            return p
    return {}


# ---------------------------------------------------------------------------
# Figura
# ---------------------------------------------------------------------------

COLORS = {
    "offered":  "#1f77b4",   # azul
    "ok":       "#2ca02c",   # verde
    "errors":   "#d62728",   # rojo
    "desired":  "#ff7f0e",   # naranja
    "healthy":  "#9467bd",   # morado
    "cpu_avg":  "#8c564b",   # marron
    "cpu_max":  "#e377c2",   # rosa
    "lat":      "#7f7f7f",   # gris
    "elb_5xx":  "#d62728",   # rojo
    "tgt_5xx":  "#ff7f0e",   # naranja
    "increase": "#d62728",
    "reduce":   "#2ca02c",
    "proactive":"#9467bd",
}

REASON_LABELS = {
    "INCREASE_CAPACITY": "↑",
    "REDUCE_CAPACITY":   "↓",
}


def add_action_markers(ax, df_dec, ymin, ymax):
    """Dibuja lineas verticales en cada ciclo donde se ejecuto una accion."""
    actions = df_dec[df_dec["result_status"] == "SUCCEEDED"]
    for _, row in actions.iterrows():
        decision = row["decision"]
        reason   = row["reason_code"]
        color = COLORS["proactive"] if reason == "PROACTIVE_RPS_TREND" else \
                COLORS["increase"] if decision == "INCREASE_CAPACITY" else \
                COLORS["reduce"]
        ax.axvline(x=row["t_seconds"], color=color, alpha=0.35, linewidth=1.2, linestyle="--")
        label = REASON_LABELS.get(decision, "")
        if label:
            ax.text(row["t_seconds"], ymax * 0.97, label,
                    color=color, fontsize=9, ha="center", va="top", fontweight="bold")


def build_decisions_table(df_dec):
    """Construye los datos para la tabla resumen de decisiones del controlador."""
    rows = []
    for _, r in df_dec.iterrows():
        decision_short = {
            "INCREASE_CAPACITY": "↑ INCREASE",
            "REDUCE_CAPACITY":   "↓ REDUCE",
            "MAINTAIN_CAPACITY": "— MANTENER",
        }.get(r["decision"], r["decision"])

        reason = r["reason_code"]
        # Marcar proactivo en la columna de razon
        if reason == "PROACTIVE_RPS_TREND":
            reason = "⬟ " + reason

        cpu  = f"{r['cpu_avg']:.1f}%" if not math.isnan(r["cpu_avg"]) else "—"
        rpt  = f"{r['rpt']:.0f}"      if not math.isnan(r["rpt"])     else "—"
        t    = f"{r['t_seconds']:.0f}s"
        cap  = f"{int(r['desired'])}" if not math.isnan(r["desired"]) else "—"
        stat = r["result_status"]

        rows.append([int(r["cycle"]), t, decision_short, reason, cap, cpu, rpt, stat])
    return rows


def plot_experiment(run_dir):
    csv_path   = os.path.join(run_dir, "loadgen.csv")
    jsonl_path = os.path.join(run_dir, "decisions.jsonl")

    missing = [p for p in (csv_path, jsonl_path) if not os.path.exists(p)]
    if missing:
        print(f"Error: faltan archivos en {run_dir}: {missing}")
        return

    # --- cargar datos ---
    df_load = pd.read_csv(csv_path)
    records  = load_decisions(jsonl_path)
    df_dec   = records_to_df(records)
    params   = load_params(records)

    if df_dec.empty:
        print(f"Advertencia: decisions.jsonl vacio en {run_dir}")
        return

    u_high = params.get("u_high", None)
    u_low  = params.get("u_low",  None)

    exp_id = ""
    if not df_load.empty and "experiment_id" in df_load.columns:
        exp_id = df_load["experiment_id"].iloc[0]

    # alinear t_seconds del loadgen al mismo t0 que las decisiones
    if "t_seconds" not in df_load.columns and "timestamp" in df_load.columns:
        t0_load = pd.to_datetime(df_load["timestamp"], utc=True).min()
        df_load["t_seconds"] = (pd.to_datetime(df_load["timestamp"], utc=True) - t0_load).dt.total_seconds()

    # --- figura: 4 graficas ---
    fig = plt.figure(figsize=(14, 12))
    fig.suptitle(f"Resultados del Experimento: {exp_id}  —  {run_dir}", fontsize=14, fontweight="bold")

    gs = fig.add_gridspec(4, 1, hspace=0.3)
    ax1 = fig.add_subplot(gs[0])
    ax2 = fig.add_subplot(gs[1], sharex=ax1)
    ax3 = fig.add_subplot(gs[2], sharex=ax1)
    ax4 = fig.add_subplot(gs[3], sharex=ax1)

    # ---- Subplot 1: RPS ----
    if not df_load.empty and "t_seconds" in df_load.columns:
        if "offered" in df_load.columns:
            ax1.plot(df_load["t_seconds"], df_load["offered"],
                     label="Ofrecidas (RPS)", color=COLORS["offered"], linestyle="--", linewidth=1.5)
        if "ok" in df_load.columns:
            ax1.plot(df_load["t_seconds"], df_load["ok"],
                     label="Exitosas", color=COLORS["ok"], linewidth=1.5)
        if "errors" in df_load.columns:
            ax1.fill_between(df_load["t_seconds"], df_load["errors"],
                             label="Errores 5xx/Timeout", color=COLORS["errors"], alpha=0.4)
    ax1.set_ylabel("RPS")
    ax1.set_title("Tráfico inyectado vs procesado")
    ax1.legend(loc="upper left", fontsize=8)
    ax1.grid(True, alpha=0.3)
    ymax1 = ax1.get_ylim()[1]
    add_action_markers(ax1, df_dec, 0, ymax1)

    # ---- Subplot 2: Capacidad ----
    ax2.step(df_dec["t_seconds"], df_dec["desired"],
             label="Deseada", color=COLORS["desired"], linewidth=2, where="post")
    ax2.step(df_dec["t_seconds"], df_dec["healthy"],
             label="Healthy targets", color=COLORS["healthy"], linewidth=1.5,
             linestyle=":", where="post")
    ax2.set_ylabel("Instancias")
    ax2.set_title("Capacidad del clúster (↑ INCREASE · ↓ REDUCE · morado = proactivo)")
    ax2.legend(loc="upper left", fontsize=8)
    ax2.grid(True, alpha=0.3)
    ax2.set_ylim(0, params.get("max_capacity", 5) + 0.5)
    add_action_markers(ax2, df_dec, 0, params.get("max_capacity", 5) + 0.5)

    # ---- Subplot 3: CPU ----
    ax3.plot(df_dec["t_seconds"], df_dec["cpu_avg"],
             label="CPU promedio (%)", color=COLORS["cpu_avg"], linewidth=1.5)
    cpu_max_col = df_dec["cpu_max"].dropna()
    if not cpu_max_col.empty and not cpu_max_col.isna().all():
        ax3.plot(df_dec["t_seconds"], df_dec["cpu_max"],
                 label="CPU máximo (%)", color=COLORS["cpu_max"], linewidth=1,
                 linestyle="-.", alpha=0.7)
    if u_high is not None:
        ax3.axhline(u_high, color="red",   linestyle="--", linewidth=1, alpha=0.7, label=f"u_high ({u_high}%)")
    if u_low is not None:
        ax3.axhline(u_low,  color="green", linestyle="--", linewidth=1, alpha=0.7, label=f"u_low ({u_low}%)")
    ax3.set_ylabel("CPU (%)")
    ax3.set_title("Utilización de CPU (promedio y máximo del ASG)")
    ax3.legend(loc="upper left", fontsize=8)
    ax3.grid(True, alpha=0.3)
    add_action_markers(ax3, df_dec, 0, ax3.get_ylim()[1] or 100)

    # ---- Subplot 4: Errores 5xx + latencia ----
    ax4b = ax4.twinx()
    has_elb = not df_dec["e5x_elb"].isna().all()
    has_tgt = not df_dec["e5x_tgt"].isna().all()
    if has_elb:
        ax4.bar(df_dec["t_seconds"], df_dec["e5x_elb"].clip(lower=0).fillna(0),
                label="5xx ELB (backlog/GIL)", color=COLORS["elb_5xx"], alpha=0.5, width=20)
    if has_tgt:
        bottom = df_dec["e5x_elb"].clip(lower=0).fillna(0) if has_elb else None
        ax4.bar(df_dec["t_seconds"], df_dec["e5x_tgt"].clip(lower=0).fillna(0),
                label="5xx Target (error de app)", color=COLORS["tgt_5xx"], alpha=0.5,
                width=20, bottom=bottom)
    if not df_load.empty and "latency_p90_s" in df_load.columns and "t_seconds" in df_load.columns:
        ax4b.plot(df_load["t_seconds"], df_load["latency_p90_s"],
                  label="Latencia p90 (s)", color=COLORS["lat"], alpha=0.7, linewidth=1)
    elif not df_dec["lat_p90"].isna().all():
        ax4b.plot(df_dec["t_seconds"], df_dec["lat_p90"],
                  label="Latencia p90 (s)", color=COLORS["lat"], alpha=0.7, linewidth=1)
    ax4.set_xlabel("Tiempo (segundos desde inicio del experimento)")
    ax4.set_ylabel("Errores 5xx (count/periodo)")
    ax4b.set_ylabel("Latencia p90 (s)")
    ax4.set_title("Errores 5xx por origen + latencia p90  [ELB=backlog lleno · Target=error de app]")
    ax4.set_ylim(bottom=0)
    lines1, labels1 = ax4.get_legend_handles_labels()
    lines2, labels2 = ax4b.get_legend_handles_labels()
    ax4.legend(lines1 + lines2, labels1 + labels2, loc="upper left", fontsize=8)
    ax4.grid(True, alpha=0.3)
    add_action_markers(ax4, df_dec, 0, max(ax4.get_ylim()[1], 1))

    fig.subplots_adjust(top=0.95, bottom=0.06, left=0.06, right=0.94)
    out_file = os.path.join(run_dir, "grafica_resultados.png")
    plt.savefig(out_file, dpi=150)
    plt.close()
    print(f"Gráfica generada: {out_file}")

    # ---- Figura Independiente: Tabla de decisiones ----
    n_dec = len(df_dec)
    table_height = max(2.0, n_dec * 0.25 + 0.5)
    fig_tbl = plt.figure(figsize=(10, table_height))
    ax_tbl = fig_tbl.add_subplot(111)
    ax_tbl.axis("off")
    #ax_tbl.set_title(f"Decisiones del Controlador: {exp_id}", fontsize=12, pad=10, fontweight="bold")

    col_labels = ["Ciclo", "t", "Decisión", "Razón", "Cap.", "CPU avg", "RPT avg", "Resultado"]
    table_data = build_decisions_table(df_dec)

    tbl = ax_tbl.table(
        cellText=table_data,
        colLabels=col_labels,
        loc="center",
        cellLoc="center"
    )
    tbl.auto_set_font_size(False)
    tbl.set_fontsize(8)
    tbl.scale(1, 1.5)
    tbl.auto_set_column_width(col=list(range(len(col_labels))))

    # Colorear filas segun la decision
    for (row_idx, col_idx), cell in tbl.get_celld().items():
        if row_idx == 0:
            cell.set_facecolor("#2c3e50")
            cell.set_text_props(color="white", fontweight="bold")
        elif row_idx <= len(table_data):
            decision_val = table_data[row_idx - 1][2]  # columna "Decisión"
            status_val   = table_data[row_idx - 1][7]  # columna "Resultado"
            reason_val   = table_data[row_idx - 1][3]  # columna "Razón"
            if "⬟" in reason_val:          # proactivo
                cell.set_facecolor("#e8d5f5")
            elif "↑" in decision_val and status_val == "SUCCEEDED":
                cell.set_facecolor("#fde8e8")
            elif "↓" in decision_val and status_val == "SUCCEEDED":
                cell.set_facecolor("#e8f5e9")
            elif row_idx % 2 == 0:
                cell.set_facecolor("#f5f5f5")
            else:
                cell.set_facecolor("#ffffff")
        cell.set_edgecolor("#cccccc")

    tbl_file = os.path.join(run_dir, "tabla_decisiones.png")
    fig_tbl.tight_layout()
    plt.savefig(tbl_file, dpi=150, bbox_inches="tight")
    plt.close()
    print(f"Tabla generada: {tbl_file}")

    # --- resumen de texto ---
    print_summary(exp_id, run_dir, df_dec, df_load, params)


def print_summary(exp_id, run_dir, df_dec, df_load, params):
    """Imprime un resumen ejecutivo del experimento en consola."""
    print("\n" + "=" * 60)
    print(f"  RESUMEN: {exp_id or run_dir}")
    print("=" * 60)

    # acciones
    succeeded = df_dec[df_dec["result_status"] == "SUCCEEDED"]
    increases = succeeded[succeeded["decision"] == "INCREASE_CAPACITY"]
    reduces   = succeeded[succeeded["decision"] == "REDUCE_CAPACITY"]
    proactive = succeeded[succeeded["reason_code"] == "PROACTIVE_RPS_TREND"]
    print(f"  Ciclos totales     : {len(df_dec)}")
    print(f"  Acciones INCREASE  : {len(increases)}"
          + (f"  (de las cuales {len(proactive)} proactivas)" if len(proactive) else ""))
    print(f"  Acciones REDUCE    : {len(reduces)}")

    # capacidad
    cap_max = df_dec["desired"].max()
    cap_min = df_dec["desired"].min()
    print(f"  Capacidad max/min  : {int(cap_max) if not math.isnan(cap_max) else '?'}"
          f" / {int(cap_min) if not math.isnan(cap_min) else '?'}")

    # CPU
    cpu_mean = df_dec["cpu_avg"].dropna().mean()
    cpu_max  = df_dec["cpu_max"].dropna().max()
    print(f"  CPU promedio (avg) : {cpu_mean:.1f}%" if not math.isnan(cpu_mean) else "  CPU promedio avg  : n/a")
    print(f"  CPU máximo (max)   : {cpu_max:.1f}%"  if not math.isnan(cpu_max)  else "  CPU máximo max    : n/a")

    # errores (desde loadgen)
    if not df_load.empty and "errors" in df_load.columns and "offered" in df_load.columns:
        total_offered = df_load["offered"].sum()
        total_errors  = df_load["errors"].sum()
        pct = (total_errors / total_offered * 100) if total_offered > 0 else 0
        print(f"  Errores totales    : {int(total_errors)} / {int(total_offered)} "
              f"({pct:.2f}%) [fuente: loadgen]")

    # errores 5xx por tipo
    elb_total = df_dec["e5x_elb"].fillna(0).sum()
    tgt_total = df_dec["e5x_tgt"].fillna(0).sum()
    if elb_total > 0 or tgt_total > 0:
        print(f"  5xx ELB (backlog)  : {elb_total:.0f}  "
              f"[firma de saturacion GIL/backlog]")
        print(f"  5xx Target (app)   : {tgt_total:.0f}  "
              f"[errores devueltos por la aplicacion]")

    # latencia
    if not df_load.empty and "latency_p90_s" in df_load.columns:
        lat = df_load["latency_p90_s"].dropna().mean()
        lat_max = df_load["latency_p90_s"].dropna().max()
        slo = params.get("reduce_max_p90_seconds", None)
        print(f"  Latencia p90 media : {lat:.3f}s | max {lat_max:.3f}s"
              + (f" | SLO reduce: {slo}s" if slo else ""))

    print("=" * 60 + "\n")


# ---------------------------------------------------------------------------
# Entry point
# ---------------------------------------------------------------------------

if __name__ == "__main__":
    if len(sys.argv) < 2:
        print("Uso: python scripts/plot_results.py <ruta_run01> [<ruta_run02> ...]")
        sys.exit(1)
    for run_dir in sys.argv[1:]:
        plot_experiment(run_dir)
