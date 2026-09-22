# Guía de Despliegue Manual en AWS 

Esta guía detalla los pasos exactos para desplegar la infraestructura completa del controlador de elasticidad horizontal directamente desde la consola web de AWS.

---

## 1. Configuración de Red (VPC y Subredes)
Asegúrate de estar trabajando en una región específica (por ejemplo, `us-east-1`).
1. Utiliza la VPC por defecto o crea una nueva.
2. Identifica al menos **dos subredes públicas** en diferentes Zonas de Disponibilidad (ej. `us-east-1a` y `us-east-1b`).

---

## 2. Creación del Balanceador de Carga y Target Group

### 2.1 Target Group
1. Ve a **EC2 -> Target Groups** y haz clic en **Create target group**.
2. **Target type:** Selecciona `Instances`.
3. **Target group name:** `app-tg`.
4. **Protocol:** `HTTP` | **Port:** `80`.
5. **Health checks:**
   - **Health check path:** `/health`
   - En Advanced health check settings:
     - **Healthy threshold:** `2`
     - **Unhealthy threshold:** `3`
     - **Timeout:** `5` seconds
     - **Interval:** `15` seconds
6. Haz clic en **Next** y luego en **Create target group** (no registres ninguna instancia aún).

### 2.2 Application Load Balancer (ALB)
1. Ve a **EC2 -> Load Balancers** y haz clic en **Create load balancer**.
2. Selecciona **Application Load Balancer**.
3. **Load balancer name:** `app-alb`.
4. **Scheme:** `Internet-facing` | **IP address type:** `IPv4`.
5. **Network mapping:** Selecciona tu VPC y las dos subredes públicas identificadas en el paso 1.
6. **Security groups:** Crea o selecciona un Security Group que permita tráfico HTTP (puerto 80) desde `0.0.0.0/0`.
7. **Listeners and routing:**
   - Protocol: `HTTP`, Port: `80`
   - Default action: Forward to `app-tg`
8. Haz clic en **Create load balancer**.

---

## 3. Configuración de la Aplicación Base

### 3.1 Launch Template para el ASG
1. Ve a **EC2 -> Launch Templates** y haz clic en **Create launch template**.
2. **Launch template name:** `app-lt`.
3. **Amazon machine image (AMI):** Selecciona **Amazon Linux 2023**.
4. **Instance type:** `t3.micro`.
5. **Key pair:** Selecciona tu par de claves SSH (ej. `vockey`).
6. **Network settings:**
   - **Security groups:** Crea o selecciona un Security Group que permita tráfico HTTP (puerto 80) desde el Security Group del ALB creado en el paso 2.2.
7. **Advanced details:**
   - **Detailed CloudWatch monitoring:** Selecciona **Enable** (Crucial para tener métricas de 1 minuto).
   - **User data:** Pega el contenido exacto del archivo `deploy/app_userdata.sh` de tu repositorio local.
8. Haz clic en **Create launch template**.

---

## 4. Configuración del Auto Scaling Group (ASG)

El ASG funcionará puramente como un actuador controlado por nuestra aplicación en Go.

1. Ve a **EC2 -> Auto Scaling Groups** y haz clic en **Create Auto Scaling group**.
2. **Name:** `app-asg`.
3. **Launch template:** Selecciona `app-lt` y haz clic en Next.
4. **Network:** Selecciona tu VPC y las subredes públicas. Next.
5. **Load balancing:** 
   - Selecciona **Attach to an existing load balancer**.
   - Choose from your load balancer target groups: Selecciona `app-tg`.
   - Activa los Elastic Load Balancing health checks. Next.
6. **Group size and scaling:**
   - Desired capacity: `1`
   - Minimum capacity: `1`
   - Maximum capacity: `5`
   - **Scaling policies:** Selecciona **None** (Ninguna). Nuestro controlador hará el trabajo. Next.
7. Omite notificaciones y etiquetas, revisa y haz clic en **Create Auto Scaling group**.

---

## 5. Despliegue del Servidor del Controlador

### 5.1 Instancia EC2 del Controlador
1. Ve a **EC2 -> Instances** y haz clic en **Launch instances**.
2. **Name:** `autoscaling-controller`.
3. **AMI:** Amazon Linux 2023.
4. **Instance type:** `t3.micro`.
5. **Key pair:** Tu par de claves SSH.
6. **Network settings:** Selecciona una subred pública. Asigna una IP pública automáticamente. Permite tráfico SSH (puerto 22).
7. **Advanced details:**
   - **IAM instance profile:** Selecciona **LabInstanceProfile** (si estás en AWS Academy) o un rol con permisos completos para EC2, AutoScaling y CloudWatch.
   - **User data:** Pega el contenido del archivo `deploy/controller_setup.sh`.
8. Haz clic en **Launch instance**.

### 5.2 Configuración del Archivo `config.yaml`
Una vez creados todos los recursos, debes obtener los identificadores reales de AWS y pegarlos en el archivo `config/config.yaml` en tu computadora local:

1. `target_group_arn`: El ARN completo de `app-tg`.
2. `load_balancer_dimension`: El sufijo del ARN del ALB (ej. `app/app-alb/8675e03f837d5dfe`).
3. `target_group_dimension`: El sufijo del ARN del Target Group (ej. `targetgroup/app-tg/fd5d1fcf5ed5a858`).

### 5.3 Despliegue del Binario
Finalmente, desde tu computadora local (Git Bash / WSL), sube el binario compilado y tu configuración actualizada a la instancia del controlador usando la IP pública que le asignó AWS:

```bash
./scripts/deploy.sh ec2-user@<IP_PUBLICA_CONTROLADOR> ~/.ssh/tu_clave.pem
```

El script se encargará de compilar cruzado el código en Go para Linux, subirlo por SCP y reiniciar el servicio systemd del controlador de manera automática.
