#!/usr/bin/env bash
# Ejecuta un experimento extremo a extremo contra la infraestructura real.

# run_experiment.sh E2 01 <usuario@host-controlador> <dns-del-alb> [clave-ssh]


set -euo pipefail
EXP="${1:?experimento, ej. E2}"; RUN="${2:?numero de corrida, ej. 01}"
HOST="${3:?usuario@host del controlador}"; ALB="${4:?dns del ALB}"; KEY="${5:-}"
REGION="${REGION:-us-east-1}"; ASG="${ASG:-app-asg}"
SSH=(ssh); SCP=(scp); [ -n "$KEY" ] && { SSH=(ssh -i "$KEY"); SCP=(scp -i "$KEY"); }

cd "$(dirname "$0")/.."
ID="${EXP}-run${RUN}"
OUT="results/${EXP}/run${RUN}"
mkdir -p "$OUT"
DUR=$(python3 -c "import json;print(json.load(open('loadgen/profiles/${EXP}.json'))['duration_seconds'])")

# Capacidad inicial declarada por experimento (E4 debe partir de 5 para poder bajar)
case "$EXP" in E4) INIT=5 ;; E5) INIT=2 ;; *) INIT=1 ;; esac
INIT="${INITIAL_CAPACITY:-$INIT}"

echo "== estado inicial declarado: ${INIT} instancia(s) =="
aws autoscaling set-desired-capacity --auto-scaling-group-name "$ASG" \
  --desired-capacity "$INIT" --region "$REGION" || true
echo "== esperando capacidad estable (${INIT}) =="
for _ in $(seq 1 60); do
  TOTAL=$(aws autoscaling describe-auto-scaling-groups --auto-scaling-group-names "$ASG" \
    --region "$REGION" --query "length(AutoScalingGroups[0].Instances)" --output text || echo -1)
  INSVC=$(aws autoscaling describe-auto-scaling-groups --auto-scaling-group-names "$ASG" \
    --region "$REGION" --query "length(AutoScalingGroups[0].Instances[?LifecycleState=='InService'])" --output text || echo -1)
  [ "$TOTAL" = "$INIT" ] && [ "$INSVC" = "$INIT" ] && break
  sleep 10
done
sleep 45   # health checks del ALB (intervalo 15 s x 2 exitos)

echo "== reiniciando el controlador con experiment_id=${ID} =="
"${SSH[@]}" "$HOST" "sudo sed -i 's/^experiment_id:.*/experiment_id: ${ID}/' /etc/autoscaling-controller/config.yaml
  sudo truncate -s 0 /var/log/autoscaling-controller/decisions.jsonl
  sudo systemctl restart autoscaling-controller"

START=$(date -u +%Y-%m-%dT%H:%M:%SZ)
echo "== generando carga con k6 (${DUR}s) =="

# Convertir el perfil JSON al formato de stages de k6 y obtener el RPS inicial
K6_START_RATE=$(python3 -c "
import json
p = json.load(open('loadgen/profiles/${EXP}.json'))
print(int(p['points'][0]['rps']))
")

K6_STAGES=$(python3 -c "
import json
p = json.load(open('loadgen/profiles/${EXP}.json'))
pts = p['points']
stages = []
for a, b in zip(pts, pts[1:]):
    dur = b['t'] - a['t']
    stages.append({'duration': f'{dur}s', 'target': int(b['rps'])})
print(json.dumps(stages))
")

# k6 puede correr en una EC2 dedicada (recomendado) o en esta maquina
#   LOADGEN_HOST=ec2-user@<ip>   -> k6 remoto (ver `terraform output loadgen_host`)
#   LOADGEN_KEY=<clave.pem>      -> clave SSH del generador (por defecto, la del controlador)
#   CONN_CLOSE=1                 -> desactiva keep-alive (Connection: close)
LOADGEN_HOST="${LOADGEN_HOST:-}"
LOADGEN_KEY="${LOADGEN_KEY:-$KEY}"

if [ -n "$LOADGEN_HOST" ]; then
  LSSH=(ssh -o ServerAliveInterval=30 -o StrictHostKeyChecking=accept-new)
  LSCP=(scp -o StrictHostKeyChecking=accept-new)
  [ -n "$LOADGEN_KEY" ] && { LSSH+=(-i "$LOADGEN_KEY"); LSCP+=(-i "$LOADGEN_KEY"); }

  echo "== esperando al generador de carga ${LOADGEN_HOST} =="
  READY=0
  for _ in $(seq 1 30); do
    if "${LSSH[@]}" -o ConnectTimeout=5 "$LOADGEN_HOST" 'test -f ~/lg/.ready && command -v k6 >/dev/null' 2>/dev/null; then
      READY=1; break
    fi
    sleep 10
  done
  [ "$READY" = 1 ] || { echo "el generador de carga no esta listo (revisa /var/log/cloud-init-output.log)"; exit 1; }

  STAGES_FILE="$(mktemp)"
  printf '%s' "$K6_STAGES" > "$STAGES_FILE"
  "${LSCP[@]}" loadgen/loadgen.js loadgen/k6_to_csv.py "loadgen/profiles/${EXP}.json" "$STAGES_FILE" "$LOADGEN_HOST:lg/"
  "${LSSH[@]}" "$LOADGEN_HOST" "mv ~/lg/$(basename "$STAGES_FILE") ~/lg/stages.json"
  rm -f "$STAGES_FILE"

  # Los \$ se evaluan en la EC2; el resto se expande aqui
  "${LSSH[@]}" "$LOADGEN_HOST" bash -s <<REMOTE
set -euo pipefail
cd ~/lg
ulimit -n 65535 || true
rm -f k6_raw.csv
TARGET_URL="http://${ALB}/" LOADGEN_STAGES="\$(cat stages.json)" LOADGEN_START_RATE="${K6_START_RATE}" CONN_CLOSE="${CONN_CLOSE:-0}" \
  k6 run --out csv=k6_raw.csv --quiet loadgen.js < /dev/null
python3 k6_to_csv.py --k6csv k6_raw.csv --profile "${EXP}.json" --experiment "${ID}" --out loadgen.csv
REMOTE

  # Solo se trae el CSV agregado 
  "${LSCP[@]}" "$LOADGEN_HOST:lg/loadgen.csv" "${OUT}/loadgen.csv"
else
  K6_RAW="k6_raw.csv"
  TARGET_URL="http://${ALB}/" \
  LOADGEN_STAGES="${K6_STAGES}" \
  LOADGEN_START_RATE="${K6_START_RATE}" \
    k6 run --out "csv=${K6_RAW}" --quiet loadgen/loadgen.js

  python3 loadgen/k6_to_csv.py \
    --k6csv   "${K6_RAW}" \
    --profile "loadgen/profiles/${EXP}.json" \
    --experiment "${ID}" \
    --out     "${OUT}/loadgen.csv"
fi

END=$(date -u +%Y-%m-%dT%H:%M:%SZ)

echo "== recolectando evidencia =="
"${SCP[@]}" "$HOST:/var/log/autoscaling-controller/decisions.jsonl" "${OUT}/decisions.jsonl"
"${SCP[@]}" "$HOST:/etc/autoscaling-controller/config.yaml"         "${OUT}/config-usada.yaml"

aws autoscaling describe-scaling-activities --auto-scaling-group-name "$ASG" \
  --region "$REGION" --output json > "${OUT}/scaling-activities.json"

# Dimensiones para las metricas de CloudWatch (extraidas de la config del controlador)
TG_DIM=$(python3 -c "import yaml; print(yaml.safe_load(open('${OUT}/config-usada.yaml'))['aws']['target_group_dimension'])" 2>/dev/null || echo "")
LB_DIM=$(python3 -c "import yaml; print(yaml.safe_load(open('${OUT}/config-usada.yaml'))['aws']['load_balancer_dimension'])" 2>/dev/null || echo "")

# Metricas del ASG
aws cloudwatch get-metric-statistics --namespace "AWS/EC2" --metric-name "CPUUtilization" \
  --dimensions Name=AutoScalingGroupName,Value="$ASG" \
  --start-time "$START" --end-time "$END" --period 60 --statistics Average Maximum \
  --region "$REGION" --output json > "${OUT}/cw-CPUUtilization.json" || true

# Metricas del ALB
for NAME in "RequestCountPerTarget" "TargetResponseTime"; do
  STAT="Average"
  [ "$NAME" = "RequestCountPerTarget" ] && STAT="Sum"
  aws cloudwatch get-metric-statistics --namespace "AWS/ApplicationELB" --metric-name "$NAME" \
    --dimensions Name=TargetGroup,Value="$TG_DIM" Name=LoadBalancer,Value="$LB_DIM" \
    --start-time "$START" --end-time "$END" --period 60 --statistics "$STAT" Maximum \
    --region "$REGION" --output json > "${OUT}/cw-${NAME}.json" || true
done

aws cloudwatch get-metric-statistics --namespace "AWS/ApplicationELB" --metric-name "HealthyHostCount" \
  --dimensions Name=TargetGroup,Value="$TG_DIM" Name=LoadBalancer,Value="$LB_DIM" \
  --start-time "$START" --end-time "$END" --period 60 --statistics Average Maximum \
  --region "$REGION" --output json > "${OUT}/cw-HealthyHostCount.json" || true

cat > "${OUT}/manifest.json" <<JSON
{"experiment_id":"${ID}","started_at":"${START}","ended_at":"${END}",
 "asg":"${ASG}","region":"${REGION}","alb":"${ALB}",
 "commit":"$(git rev-parse --short HEAD 2>/dev/null || echo nogit)"}
JSON
echo "evidencia en ${OUT}"