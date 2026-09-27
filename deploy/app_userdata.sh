#!/bin/bash
# Aplicacion web de prueba.
# Se usa Gunicorn (WSGI) con multiples workers (procesos) para evadir el GIL
# de Python y aprovechar todos los nucleos reales de la instancia EC2.
set -euxo pipefail

# Instalar Python y dependencias
dnf install -y python3 python3-pip >/dev/null 2>&1 || yum install -y python3 python3-pip
pip3 install gunicorn

cat > /opt/app.py <<'PY'
import hashlib, os

WORK = int(os.environ.get("WORK_ITERATIONS", "6000"))

def application(environ, start_response):
    path = environ.get('PATH_INFO', '')
    if path.startswith("/health"):
        start_response('200 OK', [('Content-Type', 'text/plain')])
        return [b"ok"]
        
    # Trabajo sintetico dependiente de CPU
    d = b"x"
    for _ in range(WORK):
        d = hashlib.sha256(d).digest()
        
    start_response('200 OK', [('Content-Type', 'text/plain')])
    return [b"done " + d[:4].hex().encode()]
PY

cat > /etc/systemd/system/app.service <<'UNIT'
[Unit]
Description=demo web app
After=network.target

[Service]
Environment=WORK_ITERATIONS=6000
WorkingDirectory=/opt
# Lanzar gunicorn con 4 workers (procesos independientes)
# Esto evade el GIL y aprovecha multiples nucleos, permitiendo que la metrica
# de CPU sea un reflejo preciso de la saturacion real de la instancia.
ExecStart=/usr/local/bin/gunicorn -w 4 -b 0.0.0.0:80 app:application
Restart=always

[Install]
WantedBy=multi-user.target
UNIT

systemctl daemon-reload
systemctl enable --now app