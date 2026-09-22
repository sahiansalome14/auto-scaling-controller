# Auto-scaling-controller


Lazo de control realimentado que observa una aplicación web en AWS a través de
CloudWatch y decide cada 30 s entre `MAINTAIN_CAPACITY`, `INCREASE_CAPACITY` y
`REDUCE_CAPACITY`, dentro de 1 a 5 instancias EC2.

No se usan políticas de dynamic scaling de AWS. El Auto Scaling Group es
**solo el actuador** (`SetDesiredCapacity`); la lógica de decisión está en
`cmd/controller/policy.go`.

## Estructura del repositorio

```
cmd/controller/         binario del controlador (package main)
  config.go             tipos compartidos + Config + Validate()
  observe.go            observe() + validateMetrics()
  policy.go             decide() + safeToReduce() + helpers
  aws.go                fetchMetrics() + fetchCapacity() + setDesiredCapacity()
  main.go               main() + runCycle() + writeLog()

config/config.yaml      parámetros del experimento y credenciales/recursos de AWS
deploy/
  app_userdata.sh       user-data de las instancias de la aplicación web (Launch Template)
  controller_setup.sh   user-data de la instancia del controlador (EC2)
loadgen/                generador de carga + perfiles E2, E4, E5, E7
scripts/                build.sh, deploy.sh, run_experiment.sh, verify_compliance.sh
results/                evidencia experimental recolectada (E2, E4, E5, E7)
```

## La política en una cascada

El orden es parte del diseño: las guardas de seguridad van primero

| # | Condición | Decisión |
|---|---|---|
| 1 | Datos insuficientes (falta CPU, pocas muestras, dato viejo, fallo de AWS) | MAINTAIN |
| 2 | Capacidad en transición (pending/terminating, o sanos < deseados) | MAINTAIN |
| 3 | Cooldown activo tras la última acción | MAINTAIN |
| 4 | CPU > `u_high` en `k_up` de las últimas `n_samples` muestras | INCREASE |
| 5 | CPU < `u_low` en `k_down` de `n_samples` **y** reducir es seguro | REDUCE |
| 6 | Cualquier otro caso | MAINTAIN |

Reducir es seguro si (a) la CPU proyectada con una instancia menos
(`cpu · n / (n−1)`) queda bajo `u_high` y (b) la latencia p90 está bajo
`reduce_max_p90_seconds`.

---

## Experimentos

Para ejecutar cualquier experimento automatizado:
```bash
./scripts/run_experiment.sh E2 01 ec2-user@<IP_CONTROLADOR> <DNS_DEL_ALB> ~/.ssh/tu-clave.pem
```

| Exp | Perfil | Qué demuestra |
|---|---|---|
| **E2** | Rampa ascendente | Subida limpia de capacidad (`INCREASE_CAPACITY`) ante sobrecarga sostenida. |
| **E4** | Rampa descendente | Reducción limpia (`REDUCE_CAPACITY`) verificando `safeToReduce()` sin violar el SLO. |
| **E5** | Pico transitorio (120 s) | Rechazo de transitorios: cero acciones de escalado gracias a $k_{up}=3$. |
| **E7** | Rampa con resiliencia | Tolerancia a fallos y auditoría continua en régimen estable. |

Al terminar cada experimento, los resultados se guardan automáticamente en `results/<EXP>/run<NN>/`.

---

### Despliegue en AWS
ver en docs

## Verificación de cumplimiento

Ejecuta con tus credenciales de AWS:
```bash
./scripts/verify_compliance.sh app-asg us-east-1
```
Comprueba:
- Cero políticas de escalado dinámico en el ASG (`Policies: []`).
- Límites estrictos `Min=1, Max=5`.
- Cooldown en 0 en el ASG (manejado por el controlador en software).
