#!/usr/bin/env bash
# Sube el binario compilado a la instancia del controlador y reinicia el servicio

#   ./scripts/deploy.sh ec2-user@<IP> ~/.ssh/clave.pem
set -euo pipefail
HOST="${1:?uso: deploy.sh <usuario@host> [clave-ssh]}"
KEY="${2:-}"
SSH=(ssh); SCP=(scp)
[ -n "$KEY" ] && { SSH=(ssh -i "$KEY"); SCP=(scp -i "$KEY"); }

cd "$(dirname "$0")/.."
./scripts/build.sh

"${SCP[@]}" bin/autoscaling-controller "$HOST:/tmp/autoscaling-controller"
"${SCP[@]}" config/config.yaml         "$HOST:/tmp/config.yaml"
"${SSH[@]}" "$HOST" 'sudo install -m 0755 /tmp/autoscaling-controller /usr/local/bin/autoscaling-controller'
"${SSH[@]}" "$HOST" 'sudo cp /tmp/config.yaml /etc/autoscaling-controller/config.yaml'
"${SSH[@]}" "$HOST" 'echo "CONTROLLER_ARGS=" | sudo tee /etc/autoscaling-controller/env > /dev/null'
"${SSH[@]}" "$HOST" 'sudo systemctl restart autoscaling-controller && sleep 3 && sudo systemctl status --no-pager autoscaling-controller | head -12'
