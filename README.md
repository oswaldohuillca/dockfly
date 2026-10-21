# Dockfly

Panel para publicar aplicaciones detrás de **Traefik** con SSL automático, al estilo de
Nginx Proxy Manager. Todo va en **una sola imagen** (`scratch`, ~54 MB comprimida) con dos
binarios: `dockfly` (API en Go + frontend React incrustado) y `traefik`.

## Uso

```bash
docker compose up -d --build
```

Abre `http://TU_SERVIDOR:81` y crea la cuenta de administrador.

| Puerto | Uso |
|---|---|
| 80 / 443 | Tráfico de las aplicaciones (Traefik) |
| 81 | Panel de Dockfly |

### Destinos de un proxy host

- **Contenedor Docker**: eliges el contenedor de una lista. Si no comparte red con Dockfly,
  se conecta automáticamente a la red `dockfly`.
- **Este servidor**: apps fuera de Docker vía `host.docker.internal` (requiere el
  `extra_hosts` del compose; la app debe escuchar en una IP accesible desde Docker).
- **IP / URL externa**: cualquier IP o dominio. Desactiva *Conservar el Host original* si el
  destino exige su propio dominio (CDN, SaaS).

### Sitios estáticos

Al estilo de Cloudflare Pages: creas un sitio con sus dominios y subes la carpeta de tu build
(`dist/`, `build/`…) o un `.zip`, arrastrándolos al panel. Cada subida es un **despliegue
inmutable** que se publica al instante; para hacer **rollback** basta con publicar uno anterior.
Se conservan los 10 más recientes por sitio.

- URLs limpias: `/about` sirve `about.html` o `about/index.html`.
- `404.html` personalizado, o modo **SPA** (cualquier ruta sirve `index.html`).
- Compresión gzip/brotli, SSL de Let's Encrypt y ETag por despliegue.
- Los archivos que empiezan por punto (`.env`, `.git`…) nunca se sirven, salvo `.well-known`.
- **`_redirects` y `_headers`** en la raíz del build, con el formato de Cloudflare Pages:

  ```
  # _redirects
  /viejo        /nuevo           301
  /blog/*       /noticias/:splat 301
  /app/*        /app/index.html  200   # reescritura: misma URL, otro archivo
  ```
  ```
  # _headers
  /assets/*
    Cache-Control: public, max-age=31536000, immutable
  ```
  Las redirecciones se aplican antes que los archivos; las reescrituras (200), solo si el
  archivo pedido no existe.

Los archivos los sirve un servidor interno de Dockfly (`127.0.0.1:8099`) al que Traefik reenvía
los dominios de cada sitio; no hace falta ningún contenedor extra.

### Despliegue desde GitHub y GitLab

Conecta un repositorio y cada push a la rama elegida construye el sitio y lo publica.

- **GitHub**: en *Integraciones → Conectar con GitHub*, GitHub crea con un clic la app
  «Dockfly» de esta instalación (ya configurada) y te deja elegir la cuenta u organización
  y los repositorios. Dockfly solo pide permiso de lectura del código (y de escritura de los
  estados de commit, para marcar cada build con ✓/✗) y usa tokens que caducan cada hora.
  Funciona también con GitHub Enterprise.
- **GitLab**: la aplicación OAuth se registra una vez en GitLab (el panel indica los pasos y
  la Redirect URI); después, conectar una cuenta es pulsar «Autorizar». Funciona con
  gitlab.com y con instancias propias.
- **Tokens de acceso personal**: opción avanzada, para entornos sin navegador.

Cada sitio tiene una **caché de dependencias** (bun, npm, yarn, pnpm) que se conserva entre builds:
en la prueba, `bun install` pasa de ~6 s a ~40 ms en el segundo build.

Los builds corren en un contenedor efímero (`oven/bun:1` por defecto, configurable por sitio)
**sin privilegios** y con límites de memoria, CPU, procesos y tiempo. El código lo descarga
Dockfly y lo copia dentro: **las credenciales nunca entran en el contenedor de build**.

Para desplegar en cada push, GitHub/GitLab deben poder llegar a Dockfly: publica el panel con
un proxy host y escribe su dirección en *Integraciones → URL pública*. Sin ella, los sitios se
despliegan con el botón «Desplegar ahora».

Cada build marca su commit con el resultado (✓/✗ en el commit y en sus pull requests), con un
enlace a la web publicada o, si falla, al panel. Las GitHub Apps creadas con versiones
anteriores de Dockfly no tienen ese permiso: acéptalo en *GitHub → Settings → Applications →
Dockfly → Permissions* (los builds funcionan igual sin él).

#### Previews por rama

Con un **dominio de previews** (*Ajustes → Previews por rama*, p. ej. `previews.midominio.com`) y
las previews activadas en el sitio, cada push a una rama que no es la de producción se publica en
`rama--sitio.previews.midominio.com` sin tocar producción, y se borra al borrar la rama (por ejemplo,
al fusionar el pull request). Basta un registro DNS `*.previews.midominio.com` apuntando al servidor.
Para HTTPS hace falta un certificado comodín: configura la zona DNS en *Ajustes → Certificados* o
sube un certificado propio. Las previews se pueden proteger con una lista de acceso. Las pull
requests desde forks no se construyen: su código no es de confianza.

### Usuarios y roles

En *Ajustes → Usuarios* se invita al equipo con un enlace de un solo uso (válido 7 días), que
el administrador envía como prefiera. Cada usuario tiene un rol:

| Rol | Puede |
| --- | --- |
| Administrador | Todo: proxy hosts, certificados, integraciones, notificaciones, ajustes y usuarios |
| Despliegues | Sitios, apps, builds y despliegues, incluidas sus variables de entorno |
| Solo lectura | Ver el panel y los logs, sin cambiar nada ni ver secretos |

Si alguien olvida su contraseña, el administrador le genera un enlace para restablecerla. Cambiar
la contraseña cierra las demás sesiones del usuario. Siempre queda al menos un administrador.

### Tokens de API y CLI

En *Mi cuenta → Tokens de API* se crean tokens para desplegar desde la terminal o la integración
continua. Cada token tiene un rol (nunca más que el de su usuario) y una caducidad, y se puede
revocar en cualquier momento. El mismo binario `dockfly` es el CLI:

```sh
export DOCKFLY_URL=https://dockfly.midominio.com DOCKFLY_TOKEN=dfy_…
dockfly deploy ./dist --site mi-web
```

Los binarios para Linux, macOS y Windows están en cada release; también se puede usar la imagen:

```yaml
# .github/workflows/deploy.yml
- run: npm ci && npm run build
- run: >
    docker run --rm -v "$PWD/dist:/dist" -e DOCKFLY_URL -e DOCKFLY_TOKEN
    ghcr.io/<propietario>/dockfly deploy /dist --site mi-web
  env:
    DOCKFLY_URL: https://dockfly.midominio.com
    DOCKFLY_TOKEN: ${{ secrets.DOCKFLY_TOKEN }}
```

Con un token no se puede cambiar la contraseña, la verificación en dos pasos ni crear otros tokens.

### Seguridad de Docker

Las apps de **Compose** se despliegan con un contenedor auxiliar con la CLI de Docker. Ese
auxiliar no recibe el socket de Docker: habla con un **proxy con permisos mínimos** de Dockfly,
por una red interna propia de cada despliegue, que rechaza lo que daría el control del servidor:

- contenedores `privileged`, `cap_add`, `devices`/GPUs, `network_mode: host` y namespaces del host;
- montar rutas sensibles del host (`/`, `/etc`, `/home`, `/root`, sockets…) o los datos de Dockfly;
- tocar contenedores, volúmenes o redes de otras apps (o del propio Dockfly), y los `prune`.

Si un `docker-compose.yml` necesita algo de eso, Compose muestra el motivo en el log del
despliegue (`Dockfly: los contenedores privilegiados… no están permitidos`).

Para no darle a Dockfly el socket completo, puede ir detrás de
[docker-socket-proxy](https://github.com/Tecnativa/docker-socket-proxy):

```yaml
services:
  socket-proxy:
    image: tecnativa/docker-socket-proxy
    volumes: ["/var/run/docker.sock:/var/run/docker.sock:ro"]
    environment:
      CONTAINERS: 1
      IMAGES: 1
      NETWORKS: 1
      VOLUMES: 1
      BUILD: 1
      EXEC: 1
      INFO: 1
      POST: 1
      ALLOW_START: 1
      ALLOW_STOP: 1
      ALLOW_RESTARTS: 1
  dockfly:
    image: ghcr.io/<propietario>/dockfly
    environment:
      DOCKFLY_DOCKER_SOCKET: tcp://socket-proxy:2375
    # …sin montar /var/run/docker.sock
```

Con docker-socket-proxy, los `build:` de los `docker-compose.yml` no funcionan: BuildKit
necesita un canal (`/grpc`) que su HAProxy no reenvía. Las apps con Dockerfile y las imágenes
ya construidas funcionan igual.

### Verificación en dos pasos

En *Mi cuenta* (menú del usuario) se activa la verificación en dos pasos con cualquier app TOTP
(Google Authenticator, 1Password, Bitwarden…). Al activarla, Dockfly muestra 10 **códigos de
recuperación** de un solo uso. Si pierdes el móvil y los códigos, desactívala desde el servidor:

```sh
docker exec dockfly dockfly reset-2fa tu@email.com
```

### Tráfico

Cada proxy host, sitio y app muestra sus **peticiones** (por estado: 2xx/3xx, 4xx, 5xx) y su
**latencia** (p50 y p95) de la última hora, el último día o la última semana; la lista de proxy
hosts incluye una minigráfica de las últimas 24 horas. Los datos salen de las métricas de Traefik
(que solo escuchan en `127.0.0.1` dentro del contenedor), se recogen cada minuto y se guardan 7 días.

### Varios servidores

En *Servidores → Añadir servidor*, Dockfly da un comando para ejecutar en otra máquina con Docker:

```sh
docker run -d --name dockfly-agent --restart unless-stopped \
  -p 80:80 -p 443:443 \
  -v /var/run/docker.sock:/var/run/docker.sock -v dockfly-agent:/data \
  ghcr.io/<propietario>/dockfly agent --join https://dockfly.midominio.com --token dfa_…
```

El **agente** se conecta al panel (no hay que abrir puertos en el servidor, funciona tras NAT) y
ejecuta su propio Traefik. Al crear una app se elige en qué servidor corre: el panel la despliega
en él por el túnel del agente (builds de Dockerfile, despliegues sin corte, logs, copias de las
bases de datos…) y le envía sus rutas. Sus dominios deben apuntar a la IP de ese servidor, que
pide sus propios certificados. **Si el panel se cae, las apps de los servidores siguen sirviendo**;
al volver, el agente se reconecta solo. Un servidor desconectado se notifica.

Por ahora, los proxy hosts, los sitios estáticos y las apps de Compose van en el servidor del panel.

### Copias de seguridad

En *Ajustes → Copias de seguridad* se descarga una copia con la base de datos, las claves, los
sitios publicados y los certificados, y se restaura subiéndola (también desde la pantalla de
primera instalación, para migrar de servidor). Desde la terminal:

```bash
docker exec dockfly dockfly backup /data/copia.tar.gz   # crear
docker exec dockfly dockfly restore /data/copia.tar.gz  # preparar; se aplica al reiniciar
```

> Copiar `dockfly.db` a mano no basta: SQLite trabaja en modo WAL y los cambios recientes están
> en `dockfly.db-wal`. La copia de Dockfly usa `VACUUM INTO`, que sí es consistente.

### Apps

Contenedores que Dockfly despliega y gestiona: APIs, bases de datos, servicios… desde una
**imagen** (`postgres:17`, `ghcr.io/usuario/app`) o desde un **Dockerfile** de un repo de
GitHub/GitLab, que se reconstruye en cada push. Hay plantillas (un clic) para PostgreSQL, MySQL,
Redis, n8n y Uptime Kuma.

- **Sin corte**: el contenedor nuevo arranca, pasa la comprobación de salud (ruta HTTP,
  `HEALTHCHECK` de la imagen o puerto abierto) y solo entonces recibe el tráfico. Si falla, la
  versión anterior sigue sirviendo y el log muestra la salida del contenedor.
- **Con volúmenes** (bases de datos) la versión actual se detiene antes de arrancar la nueva:
  dos instancias escribiendo en los mismos datos los corromperían.
- Las apps se encuentran por su nombre en la red interna (`postgres:5432`), sin exponerlas.
- También desde un **`docker-compose.yml`** del repo: Dockfly lo ejecuta con un contenedor auxiliar
  con Compose y conecta el servicio web a su red. Usa volúmenes con nombre: las rutas relativas
  del host (`./datos:/datos`) no funcionan.
- Variables de entorno cifradas, límites de memoria y CPU, logs en vivo, reiniciar, detener y
  volver a una versión anterior.

#### Copias de las bases de datos

Las apps de **PostgreSQL**, **MySQL/MariaDB** y **Redis** tienen la pestaña *Copias*: copias
completas (`pg_dumpall`, `mysqldump`, `redis-cli --rdb`) comprimidas, programadas cada hora, día
o semana, conservando las N últimas, y con botón «Copia ahora». Se descargan y, las de
PostgreSQL y MySQL, se restauran desde el panel. Las copias fallidas se notifican.

Se guardan en el servidor (`/data/db-backups`) o, mejor, en un bucket compatible con **S3**
(AWS, Cloudflare R2, Backblaze B2, MinIO…), configurado en *Ajustes → Copias de las bases de
datos*: así sobreviven aunque se pierda el servidor. Al guardar el bucket, Dockfly comprueba que
puede escribir en él.

## Configuración

| Variable | Por defecto | Descripción |
|---|---|---|
| `DOCKFLY_DATA_DIR` | `/data` | Base de datos, clave de sesión, config y certificados de Traefik |
| `DOCKFLY_LISTEN` | `:81` | Dirección del panel |
| `DOCKFLY_ACME_EMAIL` | — | Email para Let's Encrypt (opcional) |
| `DOCKFLY_ACME_STAGING` | `false` | Usa el entorno de pruebas de Let's Encrypt |
| `DOCKFLY_NETWORK` | `dockfly` | Red Docker compartida con las apps |
| `DOCKFLY_DOCKER_SOCKET` | `/var/run/docker.sock` | Socket de Docker, o `tcp://host:puerto` detrás de un proxy del socket |
| `DOCKFLY_COMPOSE_PROXY` | `1` | `0` desactiva el proxy con permisos mínimos de las apps de Compose (no recomendado) |
| `DOCKFLY_TRAEFIK` | `on` | `off` para no lanzar Traefik (desarrollo) |
| `DOCKFLY_TRUSTED_PROXIES` | — | Rangos CIDR (separados por comas) de los proxies que tienen delante el panel. Solo de ellos se acepta `X-Forwarded-For`. Vacío = panel expuesto directamente |
| `DOCKFLY_BUILD_TIMEOUT_MIN` | `15` | Tiempo máximo de un build |
| `DOCKFLY_BUILD_MEMORY_MB` / `DOCKFLY_BUILD_CPUS` | `2048` / `2` | Recursos de cada build |
| `DOCKFLY_BUILD_CONCURRENCY` | `1` | Builds simultáneos |
| `DOCKFLY_MAX_UPLOAD_MB` | `512` | Tamaño máximo de una subida de sitio estático |
| `DOCKFLY_STATIC_ADDR` | `127.0.0.1:8099` | Dirección interna del servidor de sitios estáticos |
| `DOCKFLY_LOG_LEVEL` / `DOCKFLY_TRAEFIK_LOG_LEVEL` | `info` / `warn` | Nivel de logs |

## Arquitectura

```
cmd/dockfly        punto de entrada: API, supervisor de Traefik, healthcheck
internal/api       API REST (chi) + sirve el frontend
internal/store     modelos y consultas con Bun sobre SQLite (Go puro, sin CGO)
  └─ migrations/   migraciones SQL versionadas, incrustadas en el binario
internal/pages     sitios estáticos: subida segura (zip/carpeta), despliegues y servidor
internal/apps      apps: despliegues sin corte, salud, volúmenes y ciclo de vida
internal/builder   cola de builds y ejecución en contenedores efímeros
internal/gitprovider  APIs de GitHub/GitLab: repos, código, webhooks, GitHub App y OAuth
internal/gitauth   credenciales vigentes por conexión (tokens de instalación, refresco OAuth)
internal/secrets   cifrado AES-GCM de tokens y variables de entorno
internal/traefik   genera traefik.yml (estático) y dockfly.yml (dinámico); supervisa el proceso
internal/docker    cliente mínimo del socket de Docker (sin SDK)
web/               frontend React 19 + TypeScript + Tailwind 4 + shadcn/ui, incrustado con go:embed
  └─ src/routes/   rutas de TanStack Router basadas en archivos (una carpeta = un segmento de la URL)
```

La base de datos es la única fuente de verdad: en cada cambio (y al arrancar) se regenera
`/data/traefik/dynamic/dockfly.yml` de forma atómica y Traefik lo recarga en caliente
(~200 ms) sin reiniciarse.

> ⚠️ Montar el socket de Docker da control total del host. Protege el puerto 81
> (firewall o VPN, o publica el panel detrás de un proxy host con SSL).

## Imágenes publicadas

Cada versión se publica en `ghcr.io/<propietario>/dockfly` para **amd64 y arm64** (Raspberry
Pi, instancias ARM…): `:latest` y `:1.2.3` para las versiones, `:edge` para `main`.

## Desarrollo

```bash
make dev                  # backend en :8181 sin Traefik
cd web && npm run dev     # frontend con recarga en caliente (proxy /api → :8181)
make test
```

### Idiomas

El panel está en **inglés** (por defecto) y **español**; cada usuario lo cambia en su menú. Los
textos se escriben en inglés en el código con las macros de [Lingui](https://lingui.dev)
(`<Trans>…</Trans>`, ``t`…` ``) y sus traducciones están en `web/src/locales/<idioma>/messages.po`:

```bash
cd web && npm run extract   # añade los textos nuevos a los catálogos (y quita los que ya no se usan)
```

Los errores de la API también se traducen en el panel: cada uno lleva un código estable
(`{"error": "Wrong email or password", "code": "bad_credentials"}`) y el mensaje en inglés como
respaldo. Los códigos se definen en Go con `newError` y se exportan a `web/src/lib/api-errors.gen.ts`:

```bash
go test ./internal/api -run TestAPIErrorsTS -update   # tras añadir o cambiar un código
cd web && npm run extract                              # y traducir los nuevos en el .po
```

El build falla si a un idioma le falta alguna traducción. Para añadir otro idioma, inclúyelo en
`web/lingui.config.ts` y en `web/src/lib/i18n.ts`, ejecuta `npm run extract` y traduce su `.po`.

## Roadmap

1. ✅ Proxy hosts con SSL automático (Docker, host, externos)
2. ✅ Sitios estáticos con subida directa, despliegues inmutables y rollback
3. ✅ Sitios estáticos desde GitHub/GitLab: build en cada push
4. ✅ Apps en contenedores desde una imagen o un Dockerfile
5. ✅ Previews por rama y estado del despliegue en los commits
