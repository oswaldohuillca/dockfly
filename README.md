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

Los archivos los sirve un servidor interno de Dockfly (`127.0.0.1:8099`) al que Traefik reenvía
los dominios de cada sitio; no hace falta ningún contenedor extra.

### Despliegue desde GitHub y GitLab

Conecta un repositorio y cada push a la rama elegida construye el sitio y lo publica.

- **GitHub**: en *Integraciones → Conectar con GitHub*, GitHub crea con un clic la app
  «Dockfly» de esta instalación (ya configurada) y te deja elegir la cuenta u organización
  y los repositorios. Dockfly solo pide permiso de lectura y usa tokens que caducan cada hora.
  Funciona también con GitHub Enterprise.
- **GitLab**: la aplicación OAuth se registra una vez en GitLab (el panel indica los pasos y
  la Redirect URI); después, conectar una cuenta es pulsar «Autorizar». Funciona con
  gitlab.com y con instancias propias.
- **Tokens de acceso personal**: opción avanzada, para entornos sin navegador.

Los builds corren en un contenedor efímero (`oven/bun:1` por defecto, configurable por sitio)
**sin privilegios** y con límites de memoria, CPU, procesos y tiempo. El código lo descarga
Dockfly y lo copia dentro: **las credenciales nunca entran en el contenedor de build**.

Para desplegar en cada push, GitHub/GitLab deben poder llegar a Dockfly: publica el panel con
un proxy host y escribe su dirección en *Integraciones → URL pública*. Sin ella, los sitios se
despliegan con el botón «Desplegar ahora».

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

## Configuración

| Variable | Por defecto | Descripción |
|---|---|---|
| `DOCKFLY_DATA_DIR` | `/data` | Base de datos, clave de sesión, config y certificados de Traefik |
| `DOCKFLY_LISTEN` | `:81` | Dirección del panel |
| `DOCKFLY_ACME_EMAIL` | — | Email para Let's Encrypt (opcional) |
| `DOCKFLY_ACME_STAGING` | `false` | Usa el entorno de pruebas de Let's Encrypt |
| `DOCKFLY_NETWORK` | `dockfly` | Red Docker compartida con las apps |
| `DOCKFLY_DOCKER_SOCKET` | `/var/run/docker.sock` | Socket de Docker |
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

## Roadmap

1. ✅ Proxy hosts con SSL automático (Docker, host, externos)
2. ✅ Sitios estáticos con subida directa, despliegues inmutables y rollback
3. ✅ Sitios estáticos desde GitHub/GitLab: build en cada push
4. Previews por rama y estado del despliegue en los commits
5. Apps en contenedores desde una imagen Docker o un Dockerfile
