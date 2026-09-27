#!/usr/bin/env bash
# bake_ami_2.sh — Hornea una AMI del controlador lista para produccion.
# (Incluye el binario y el config.yaml definitivo)
#
# Uso:
#   ./scripts/bake_ami_2.sh <key-name> <sg-id> <subnet-id> [iam-profile]
#
set -euo pipefail

KEY_NAME="${1:?uso: bake_ami_2.sh <key-name> <sg-id> <subnet-id> [iam-profile]}"
SG_ID="${2:?falta sg-id}"
SUBNET_ID="${3:?falta subnet-id}"
IAM_PROFILE="${4:-}"

REGION="${REGION:-us-east-1}"
INSTANCE_TYPE="${INSTANCE_TYPE:-t3.micro}"
KEY_FILE="${KEY_FILE:-$HOME/.ssh/${KEY_NAME}.pem}"
AMI_NAME="${AMI_NAME:-autoscaling-controller-prod-$(date +%Y%m%d)}"

cd "$(dirname "$0")/.."

# ---------------------------------------------------------------------------
# 1. Compilar
# ---------------------------------------------------------------------------
echo "==> compilando binario para Linux/amd64..."
./scripts/build.sh
BINARY="bin/autoscaling-controller"
echo "    sha256: $(sha256sum "$BINARY" | cut -d' ' -f1)"

# ---------------------------------------------------------------------------
# 2. AMI base: ultima Amazon Linux 2023 x86_64
# ---------------------------------------------------------------------------
echo "==> buscando ultima AMI de Amazon Linux 2023..."
BASE_AMI=$(aws ec2 describe-images \
  --region "$REGION" \
  --owners amazon \
  --filters \
    "Name=name,Values=al2023-ami-2023.*-x86_64" \
    "Name=virtualization-type,Values=hvm" \
    "Name=state,Values=available" \
  --query "sort_by(Images, &CreationDate)[-1].ImageId" \
  --output text)
echo "    base AMI: $BASE_AMI"

# ---------------------------------------------------------------------------
# 3. Lanzar instancia temporal
# ---------------------------------------------------------------------------
USER_DATA=$(base64 -w0 <<'BOOTSTRAP'
#!/bin/bash
set -euo pipefail
install -d -m 0755 /etc/autoscaling-controller \
                   /var/lib/autoscaling-controller \
                   /var/log/autoscaling-controller
# Crear unit de systemd
cat > /etc/systemd/system/autoscaling-controller.service <<'UNIT'
[Unit]
Description=Autoscaling controller
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
EnvironmentFile=-/etc/autoscaling-controller/env
ExecStart=/usr/local/bin/autoscaling-controller -config /etc/autoscaling-controller/config.yaml $CONTROLLER_ARGS
Restart=always
RestartSec=5
StandardOutput=journal
StandardError=journal

[Install]
WantedBy=multi-user.target
UNIT
# Arranca EN PRODUCCION (sin -dry-run)
echo 'CONTROLLER_ARGS=' > /etc/autoscaling-controller/env
systemctl daemon-reload
systemctl enable autoscaling-controller
# Marcar que el bootstrap termino
touch /tmp/bootstrap-done
BOOTSTRAP
)

echo "==> lanzando instancia temporal..."
LAUNCH_ARGS=(
  --region "$REGION"
  --image-id "$BASE_AMI"
  --instance-type "$INSTANCE_TYPE"
  --key-name "$KEY_NAME"
  --security-group-ids "$SG_ID"
  --subnet-id "$SUBNET_ID"
  --associate-public-ip-address
  --user-data "$USER_DATA"
  --tag-specifications "ResourceType=instance,Tags=[{Key=Name,Value=ami-bake-temp},{Key=Project,Value=autoscaling-controller}]"
  --query "Instances[0].InstanceId"
  --output text
)
if [ -n "$IAM_PROFILE" ]; then
  LAUNCH_ARGS+=(--iam-instance-profile "Name=$IAM_PROFILE")
fi

INSTANCE_ID=$(aws ec2 run-instances "${LAUNCH_ARGS[@]}")
echo "    instancia: $INSTANCE_ID"

cleanup() {
  echo "==> terminando instancia temporal $INSTANCE_ID..."
  aws ec2 terminate-instances --region "$REGION" --instance-ids "$INSTANCE_ID" --output text > /dev/null || true
}
trap cleanup EXIT

# ---------------------------------------------------------------------------
# 4. Esperar que la instancia este running y SSH disponible
# ---------------------------------------------------------------------------
echo "==> esperando estado running..."
aws ec2 wait instance-running --region "$REGION" --instance-ids "$INSTANCE_ID"

PUBLIC_IP=$(aws ec2 describe-instances \
  --region "$REGION" \
  --instance-ids "$INSTANCE_ID" \
  --query "Reservations[0].Instances[0].PublicIpAddress" \
  --output text)
echo "    IP publica: $PUBLIC_IP"

SSH=(ssh
  -i "$KEY_FILE"
  -o StrictHostKeyChecking=no
  -o UserKnownHostsFile=/dev/null
  -o ConnectTimeout=10
  "ec2-user@$PUBLIC_IP"
)
SCP=(scp
  -i "$KEY_FILE"
  -o StrictHostKeyChecking=no
  -o UserKnownHostsFile=/dev/null
)

echo "==> esperando SSH..."
for attempt in $(seq 1 30); do
  if "${SSH[@]}" "true" 2>/dev/null; then
    echo "    SSH disponible (intento $attempt)"
    break
  fi
  [ "$attempt" -eq 30 ] && { echo "ERROR: SSH no disponible tras 5 min"; exit 1; }
  sleep 10
done

echo "==> esperando que el bootstrap termine..."
for attempt in $(seq 1 24); do
  if "${SSH[@]}" "test -f /tmp/bootstrap-done" 2>/dev/null; then
    echo "    bootstrap listo (intento $attempt)"
    break
  fi
  [ "$attempt" -eq 24 ] && echo "advertencia: bootstrap puede no haber terminado; continuando..."
  sleep 5
done

# ---------------------------------------------------------------------------
# 5. Instalar binario y CONFIG.YAML de produccion
# ---------------------------------------------------------------------------
echo "==> subiendo binario y config.yaml..."
"${SCP[@]}" "$BINARY" "ec2-user@$PUBLIC_IP:/tmp/autoscaling-controller"
"${SSH[@]}" "sudo install -m 0755 /tmp/autoscaling-controller /usr/local/bin/autoscaling-controller"

"${SCP[@]}" "config/config.yaml" "ec2-user@$PUBLIC_IP:/tmp/config.yaml"
"${SSH[@]}" "sudo cp /tmp/config.yaml /etc/autoscaling-controller/config.yaml"

INSTALLED_HASH=$("${SSH[@]}" "sha256sum /usr/local/bin/autoscaling-controller | cut -d' ' -f1")
echo "    sha256 instalado: $INSTALLED_HASH"

# ---------------------------------------------------------------------------
# 6. Crear imagen
# ---------------------------------------------------------------------------
echo "==> creando imagen AMI '$AMI_NAME'..."
NEW_AMI=$(aws ec2 create-image \
  --region "$REGION" \
  --instance-id "$INSTANCE_ID" \
  --name "$AMI_NAME" \
  --description "Controlador listo para produccion. Build: $(date -u +%Y-%m-%dT%H:%M:%SZ)" \
  --no-reboot \
  --tag-specifications "ResourceType=image,Tags=[{Key=Project,Value=autoscaling-controller},{Key=BuiltAt,Value=$(date -u +%Y%m%d)}]" \
  --query "ImageId" \
  --output text)
echo "    ami-id: $NEW_AMI (esperando estado available...)"

for attempt in $(seq 1 90); do
  STATE=$(aws ec2 describe-images --region "$REGION" --image-ids "$NEW_AMI" \
    --query "Images[0].State" --output text 2>/dev/null || echo "pending")
  if [ "$STATE" = "available" ]; then
    echo "    AMI disponible (intento $attempt)"
    break
  fi
  [ "$attempt" -eq 90 ] && { echo "ERROR: AMI no disponible tras 15 min (estado: $STATE)"; exit 1; }
  sleep 10
done

# ---------------------------------------------------------------------------
# 7. Resultado
# ---------------------------------------------------------------------------
echo ""
echo "============================================================"
echo "  AMI PROD creada exitosamente"
echo "  ami-id : $NEW_AMI"
echo "  nombre : $AMI_NAME"
echo "  region : $REGION"
echo ""
echo "  ¡Listo! La AMI ya tiene el binario y el config.yaml real."
echo "  Al lanzar una instancia con esta AMI, el controlador arrancara"
echo "  gobernando la infraestructura al instante. No necesitas usar deploy.sh."
echo "============================================================"
