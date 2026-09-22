#!/bin/bash
# Aplicacion web de prueba
set -euxo pipefail
dnf install -y python3 >/dev/null 2>&1 || yum install -y python3

cat > /opt/app.py <<'PY'
import hashlib, os
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

WORK = int(os.environ.get("WORK_ITERATIONS", "18000"))

class H(BaseHTTPRequestHandler):
    protocol_version = "HTTP/1.1"

    def do_GET(self):
        if self.path.startswith("/health"):
            return self.reply(b"ok")
        # Trabajo sintetico dependiente de CPU: hace que CPUUtilization
        # correlacione con la tasa de peticiones.
        d = b"x"
        for _ in range(WORK):
            d = hashlib.sha256(d).digest()
        return self.reply(b"done " + d[:4].hex().encode())

    def reply(self, body):
        self.send_response(200)
        self.send_header("Content-Type", "text/plain")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def log_message(self, *a):
        pass

ThreadingHTTPServer(("0.0.0.0", 80), H).serve_forever()
PY

cat > /etc/systemd/system/app.service <<'UNIT'
[Unit]
After=network.target
[Service]
ExecStart=/usr/bin/python3 /opt/app.py
Restart=always
[Install]
WantedBy=multi-user.target
UNIT

systemctl daemon-reload
systemctl enable --now app