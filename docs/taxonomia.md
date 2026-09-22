# Taxonomía del Controlador de Elasticidad

El diseño del controlador implementado (`autoscaling-controller`) se clasifica según la taxonomía propuesta por Al-Dhuraibi et al. (2018) en Elasticity in Cloud Computing: State of the Art and Research Challenges.

Esta clasificación demuestra cómo el controlador se enmarca dentro de las categorías académicas y profesionales de los sistemas de auto-escalado.

| Dimensión | Clasificación | Justificación |
|---|---|---|
| **Dirección** | Horizontal (Scale-out/in) | Añade y retira instancias EC2 enteras del grupo de autoescalado. No altera los recursos (CPU/RAM) de las instancias existentes en vivo (Scale-up/down). |
| **Recurso** | Cómputo (CPU) / Servidores Web | Las instancias operan como servidores web en EC2 (`t3.micro`) bajo un Launch Template homogéneo. |
| **Alcance del Proveedor** | Monoproveedor (AWS) | Se integra exclusivamente con servicios de AWS: CloudWatch, EC2 Auto Scaling Groups y Application Load Balancers. |
| **Propósito** | Rendimiento y Costo | Busca mantener la latencia $p90 \le 0.6$ s (rendimiento) al mismo tiempo que evita el sobreaprovisionamiento de instancias cuando no son necesarias (costo). |
| **Modo de Operación** | Reactivo (MAPE-K Feedback Loop) | Observa métricas pasadas para tomar decisiones en el presente; no predice la carga futura proactivamente. |
| **Método de Decisión** | Reglas y Umbrales con Histéresis | Utiliza una cascada de guardas y una banda muerta (deadband) de CPU $[8\%,20\%]$, con parámetros asimétricos ($k_{down}=4 > k_{up}=3$) y un cooldown de 300s para evitar oscilaciones. |
| **Arquitectura** | Centralizado y Desacoplado | Consiste en un proceso controlador único en una VM dedicada. Utiliza el ASG únicamente como actuador externo, apagando las políticas de AWS dinámicas. |
| **Tiempo de Vida** | Discreto / Periódico | Muestreo periódico ($T=30$ s) de las métricas agregadas en CloudWatch cada 60 s. |

## Análisis de Decisiones de Diseño

La principal decisión que diferencia este controlador de una política estándar de Target Tracking de AWS es el uso de múltiples métricas simultáneamente (CPU, latencia p90 y RequestCountPerTarget) combinadas en una regla estricta que proyecta cómo se comportará la carga tras una acción de Scale-In.

La validación explícita `u_low < u_high * (min_capacity / (min_capacity + step))` evita oscilaciones matemáticas, lo que hace que el diseño del controlador no solo cumpla con la clasificación de Al-Dhuraibi, sino que implemente controles de estabilidad específicos recomendados en la literatura.
