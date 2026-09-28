#!/usr/bin/env bash

# Ejecutalo con tus credenciales de administrador (no con las del controlador)
#   ./scripts/verify_compliance.sh app-asg us-east-1

set -uo pipefail
ASG="${1:?uso: verify_compliance.sh <asg-name> [region]}"
REGION="${2:-us-east-1}"

echo "===  Politicas de dynamic scaling del ASG (debe ser []) ==="
aws autoscaling describe-policies --auto-scaling-group-name "$ASG" --region "$REGION" \
  --query 'ScalingPolicies[].PolicyName' --output json

echo "===  Acciones programadas del ASG (debe ser []) ==="
aws autoscaling describe-scheduled-actions --auto-scaling-group-name "$ASG" --region "$REGION" \
  --query 'ScheduledUpdateGroupActions[].ScheduledActionName' --output json

echo "=== Limites del grupo (deben ser Min=1, Max=5) ==="
aws autoscaling describe-auto-scaling-groups --auto-scaling-group-names "$ASG" --region "$REGION" \
  --query 'AutoScalingGroups[0].{Min:MinSize,Max:MaxSize,Desired:DesiredCapacity}' --output table
