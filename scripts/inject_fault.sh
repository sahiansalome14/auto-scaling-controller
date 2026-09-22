#!/bin/bash
# Script para simular una falla en la recolección de métricas durante el Experimento E7

# (Pregunta 5) exige evidencia de qué ocurre cuando falla una llamada a la API de AWS.

# Uso:
# 1. Inicia el experimento E7 normalmente en una terminal.
# 2. Cuando la carga esté estable (minuto 2-3), ejecuta este script en OTRA terminal.
# Ejemplo: ./scripts/inject_fault.sh ec2-user@3.226.235.139 ~/.ssh/controller.pem

set -e

HOST="${1:?uso: inject_fault.sh <usuario@host> <clave_pem>}"
KEY="${2:?uso: inject_fault.sh <usuario@host> <clave_pem>}"


echo "Inyectando fallo: deteniendo el controlador en $HOST por 65s..."


echo "[1/3] Deteniendo el servicio autoscaling-controller..."
ssh -i "$KEY" "$HOST" "sudo systemctl stop autoscaling-controller"

echo ""
echo "Fallo inyectado: El controlador está caído."
echo "Esperando 65 segundos (suficiente para 2 ciclos del controlador)..."
sleep 65

echo ""
echo "[2/3] Restaurando el servicio..."
ssh -i "$KEY" "$HOST" "sudo systemctl start autoscaling-controller"

echo "[3/3] Restauración completa"
echo "Revisa el archivo decisions.jsonl del experimento E7. Deberías encontrar líneas con:"
echo '"data_quality": "DATA_INSUFFICIENT"'
echo '"decision": "MAINTAIN_CAPACITY"'

