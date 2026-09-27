/**
 * loadgen.js — Generador de carga con k6 para los experimentos
 */

import http from 'k6/http';
import { check } from 'k6';

const stages     = JSON.parse(__ENV.LOADGEN_STAGES     || '[]');
const startRate  = parseInt(__ENV.LOADGEN_START_RATE   || '1', 10);
const targetUrl  = __ENV.TARGET_URL               || 'http://localhost/';
const connClose  = __ENV.CONN_CLOSE === '1';

export const options = {
  scenarios: {
    load: {
      executor:        'ramping-arrival-rate',
      startRate:       startRate,
      timeUnit:        '1s',
      // k6 crea VUs dinámicamente; 600 prelocalizados cubre hasta ~500 RPS
      // con latencias de hasta 1.2s. maxVUs es el techo absoluto.
      preAllocatedVUs: 600,
      maxVUs:          1200,
      stages:          stages,
    },
  },
};

export default function () {
  // Por defecto se reutilizan conexiones (keep-alive), como haria un cliente real y
  // sin abrir una conexion TCP nueva por peticion. CONN_CLOSE=1 restaura el
  // comportamiento anterior ('Connection: close').
  const res = http.get(targetUrl, {
    headers: connClose ? { 'Connection': 'close' } : {},
    timeout: '5s',
    tags: { name: 'request' },
  });
  check(res, {
    'ok': (r) => r.status >= 200 && r.status < 400,
  });
}