// Generador de carga con k6 para los experimentos

import http from 'k6/http';
import { check } from 'k6';

const stages     = JSON.parse(__ENV.LOADGEN_STAGES    || '[]');
const startRate  = parseInt(__ENV.LOADGEN_START_RATE   || '1', 10);
const targetUrl  = __ENV.TARGET_URL                  || 'http://localhost/';
const connClose  = __ENV.CONN_CLOSE === '1';

export const options = {
  scenarios: {
    load: {
      executor:        'ramping-arrival-rate',
      startRate:       startRate,
      timeUnit:        '1s',
      // VUs iniciales y límite máximo para manejar la carga
      preAllocatedVUs: 600,
      maxVUs:          1200,
      stages:          stages,
    },
  },
};

export default function () {
  // Realiza la petición GET, usa keep-alive por defecto o 'Connection: close' si se indica
  const res = http.get(targetUrl, {
    headers: connClose ? { 'Connection': 'close' } : {},
    timeout: '5s',
    tags: { name: 'request' },
  });
  // Valida que la respuesta sea exitosa (código entre 200 y 399)
  check(res, {
    'ok': (r) => r.status >= 200 && r.status < 400,
  });
}