# Taxonomía del Controlador de Elasticidad

El diseño del controlador implementado (`autoscaling-controller`) se clasifica según la taxonomía propuesta por Al-Dhuraibi et al. (2018) en *Elasticity in Cloud Computing: State of the Art and Research Challenges*.

Esta clasificación demuestra cómo el controlador se enmarca dentro de las categorías académicas de los sistemas de auto-escalado (Figura 2 del artículo citado).

| Dimensión de la Taxonomía | Clasificación del Controlador | Justificación |
|---|---|---|
| **Configuración (Configuration)** | Rígida (Rigid) | Utiliza instancias EC2 de tamaño fijo (`t3.micro`) bajo un Launch Template homogéneo, en lugar de negociar recursos de forma continua (configurable/auction). |
| **Alcance (Scope)** | Infraestructura -> VMs | El controlador opera directamente sobre la infraestructura virtual aprovisionando y destruyendo Máquinas Virtuales enteras a través de un ASG. |
| **Propósito (Purpose)** | Rendimiento (Performance), Disponibilidad y Costo | Busca mantener la latencia baja y reaccionar a errores 5xx (Rendimiento/Disponibilidad) minimizando a su vez la sobreprovisión de recursos (Costo). |
| **Modo, política (Mode, policy)** | Automático -> Híbrido (Reactivo + Proactivo) | Combina **Reactivo** (umbrales estáticos de CPU y errores) con **Proactivo** (análisis de series temporales calculando la pendiente de RPS para pre-escalar antes de la saturación). |
| **Método, acción (Method, action)** | Horizontal (Horizontal scaling) | El sistema de elasticidad añade (scale-out) o retira (scale-in) instancias enteras de cómputo del ASG en lugar de redimensionar la memoria/CPU de una instancia viva (escalado vertical). |
| **Arquitectura (Architecture)** | Centralizada (Centralized) | Consiste en un proceso controlador único y maestro que recoge métricas globales y aplica decisiones para todo el clúster. |
| **Proveedor (Provider)** | Único (Single) | Depende exclusivamente de un solo proveedor de nube (AWS) y sus APIs específicas (CloudWatch, EC2 Auto Scaling Groups, Application Load Balancers). |

## Análisis de Decisiones de Diseño

El diseño de este controlador implementa una lógica reactiva basada en **umbrales estáticos (Static thresholds)** pero los refuerza con **histéresis paramétrica** y una **banda muerta (deadband)**. 
La validación matemática explícita `u_low < u_high * (min_capacity / (min_capacity + step))` evita oscilaciones destructivas (*thrashing*). Adicionalmente, el controlador compensa su naturaleza puramente reactiva al introducir una regla proactiva de rampa de carga (`rps_slope_threshold`), ubicándolo en un punto intermedio interesante dentro de la clasificación estricta de la literatura, aunque su bucle de control principal sigue siendo mapeado como Reactivo basado en Umbrales.
