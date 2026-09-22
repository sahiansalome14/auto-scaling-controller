#!/bin/bash
# Pega este script en Advanced Details - User data al lanzar la EC2 del controlador
# en la consola web de AWS.
set -euxo pipefail

# Crear directorios del servicio
install -d -m 0755 /etc/autoscaling-controller \
                   /var/lib/autoscaling-controller \
                   /var/log/autoscaling-controller

# Crear la unidad de systemd
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

# Arranca por seguridad en dry-run (solo observa)
echo 'CONTROLLER_ARGS=-dry-run' > /etc/autoscaling-controller/env
systemctl daemon-reload
systemctl enable autoscaling-controller
