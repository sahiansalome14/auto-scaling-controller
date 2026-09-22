#!/usr/bin/env bash
# El resultado queda en bin/autoscaling-controller listo para subir con deploy.sh
set -euo pipefail
cd "$(dirname "$0")/.."
mkdir -p bin
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags "-s -w" -o bin/autoscaling-controller ./cmd/controller
sha256sum bin/autoscaling-controller

