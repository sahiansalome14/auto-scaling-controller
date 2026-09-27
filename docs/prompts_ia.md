# Prompts y Registro de Interacción con la IA — Controlador de Elasticidad Horizontal

---

## 1. Resumen de la Metodología de Interacción

Durante el desarrollo de este reto de aprendizaje, el modelo de lenguaje (IA) fue utilizado como **asistente de apoyo técnico y consulta** en las etapas de diseño, análisis de resultados y documentación. Las interacciones se organizaron en cuatro áreas principales:

1. **Diseño de arquitectura y lógica de control:** análisis del orden de las guardas, patrones de resiliencia y mecanismos para prevenir oscilaciones (*thrashing*).

2. **Calibración de umbrales y métricas:** análisis del comportamiento del servidor, selección de métricas de CloudWatch y definición de parámetros como `u_high`, `u_low`, `k_up` y `k_down`.

3. **Diagnóstico y análisis de resultados:** interpretación de los experimentos E2, E4, E5 y E7, identificando las causas de los errores 5xx y diferenciando entre problemas de lógica del controlador y límites físicos de capacidad.

4. **Consolidación y documentación académica:** apoyo en la organización de resultados, ecuaciones, tablas y evidencias en formato LaTeX.

---

## 2. Prompts Clave de Arquitectura y Código

### Prompt 1.1: Diseño de la Cascada de Guardas

**Objetivo:** Definir el orden determinista de evaluación de decisiones para garantizar que las condiciones de seguridad tengan prioridad sobre la lógica de escalamiento.

> *"Necesito diseñar la lógica de decisión en Go para un controlador de elasticidad horizontal en AWS EC2. Queremos implementar una cascada de guardas de seguridad ordenada por prioridad. ¿En qué orden exacto deberíamos evaluar: cooldown, calidad de datos insuficientes, instancias en transición (pending/terminating), instancias no sanas (health checks fallidos), tasa de errores 5xx, tendencia proactiva de peticiones y umbrales de CPU? Justifica el orden desde una perspectiva de sistemas distribuidos y resiliencia."*

### Prompt 1.2: Prevención de Oscilaciones (Thrashing) y `safeToReduce()`

**Objetivo:** Formular las condiciones necesarias para autorizar un *scale-in* sin provocar un ciclo reactivo inmediato.

> *"¿Cómo podemos evitar matemáticamente que el controlador reduzca una instancia y que inmediatamente la CPU del clúster remanente supere el umbral de subida `u_high`, provocando un ciclo de encendido y apagado (thrashing)? Escribe la fórmula para proyectar la CPU resultante en `safeToReduce()` y cómo integrar la latencia p90 como salvaguarda adicional."*

### Prompt 1.3: Guarda Proactiva por Tendencia (OLS)

**Objetivo:** Analizar un posible mecanismo proactivo para reducir el impacto del retardo de observación de CloudWatch.

> *"Queremos que el controlador no sea 100% reactivo. Ayúdame a pensar qué enfoque podría utilizarse para agregar una guarda proactiva basada en la pendiente de regresión lineal por mínimos cuadrados (OLS) sobre las últimas muestras disponibles de `RequestCountPerTarget`. Quiero entender qué datos debería tomar, cómo interpretar la pendiente, cómo definir el umbral y en qué parte del controlador tendría sentido agregar esta lógica. No hagas el código todavía; quiero entender primero el enfoque."*

---

## 3. Prompts de Análisis Crítico y Umbrales

### Prompt 2.1: El Punto Ciego del GIL (Python) y Umbrales de CPU

**Objetivo:** Analizar por qué el servidor podía presentar errores aunque la utilización promedio de CPU fuera relativamente baja.

> *"Al probar el servidor web original en Python (`ThreadingHTTPServer`), notamos que las peticiones empiezan a fallar con errores 502 cuando CloudWatch reporta apenas 20% de CPU. ¿Por qué ocurre esto? ¿Cómo afecta el Global Interpreter Lock (GIL) al backlog del socket y por qué la métrica de CPU puede no reflejar directamente la saturación real? ¿Qué cambios debemos hacer en la infraestructura (ej. Gunicorn multiproceso) y en las guardas del controlador para corregirlo?"*

### Prompt 2.2: Análisis del Experimento E5 (Pico Transitorio / Step Function)

**Objetivo:** Explicar el comportamiento del controlador durante un pico instantáneo de tráfico.

> *"En el experimento E5 (pico de 90 a 550 RPS por 2 minutos), el controlador escaló de 1 a 5 instancias, pero las nuevas máquinas llegaron cuando el pico ya había terminado. Si ya teníamos la guarda proactiva OLS, ¿por qué no actuó a tiempo? Explica qué información previa necesita la regresión para detectar una tendencia y por qué una función escalón no necesariamente puede anticiparse a partir de las muestras anteriores al cambio."*

### Prompt 2.3: Justificación del SLO de Errores vs. Límite Físico (`max_capacity=5`)

**Objetivo:** Diferenciar entre una limitación de capacidad del entorno y una falla lógica del controlador.

> *"En los experimentos E2 y E7 se observaron errores 5xx residuales a pesar de que el controlador escaló hasta 5 instancias. A partir de las mediciones obtenidas de aproximadamente 50 RPS por instancia, ¿cómo podemos demostrar que la capacidad máxima observada del clúster es cercana a 250 RPS? ¿Cómo se puede justificar que el estado `SATURATED_AT_MAX` representa el comportamiento esperado cuando se alcanza `max_capacity=5` y la demanda supera la capacidad disponible?"*

---

## 4. Prompts de Consolidación y Documentación

### Prompt 3.1: Fusión de Documentos de Diseño y Análisis Crítico en LaTeX

**Objetivo:** Consolidar el diseño y los resultados del proyecto en un documento académico estructurado.

> *"Junta el documento de diseño con el análisis crítico y conviértelo en un archivo `.tex` estructurado. Añade las tablas de decisiones, las ecuaciones del bucle de control, la clasificación taxonómica de Al-Dhuraibi et al., las preguntas de sustentación y una sección formal de conclusiones."*
