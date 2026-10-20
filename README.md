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
| `DOCKFLY_LOG_LEVEL` / `DOCKFLY_TRAEFIK_LOG_LEVEL` | `info` / `warn` | Nivel de logs |

## Arquitectura

```
cmd/dockfly        punto de entrada: API, supervisor de Traefik, healthcheck
internal/api       API REST (chi) + sirve el frontend
internal/store     modelos y consultas con Bun sobre SQLite (Go puro, sin CGO)
  └─ migrations/   migraciones SQL versionadas, incrustadas en el binario
internal/traefik   genera traefik.yml (estático) y dockfly.yml (dinámico); supervisa el proceso
internal/docker    cliente mínimo del socket de Docker (sin SDK)
web/               frontend React 19 + TypeScript + Tailwind 4 + shadcn/ui, incrustado con go:embed
```

La base de datos es la única fuente de verdad: en cada cambio (y al arrancar) se regenera
`/data/traefik/dynamic/dockfly.yml` de forma atómica y Traefik lo recarga en caliente
(~200 ms) sin reiniciarse.

> ⚠️ Montar el socket de Docker da control total del host. Protege el puerto 81
> (firewall o VPN, o publica el panel detrás de un proxy host con SSL).

## Desarrollo

```bash
make dev                  # backend en :8181 sin Traefik
cd web && npm run dev     # frontend con recarga en caliente (proxy /api → :8181)
make test
```

## Roadmap

1. ✅ Proxy hosts con SSL automático (Docker, host, externos)
2. Desplegar apps desde una imagen Docker (labels de Traefik, variables de entorno, logs)
3. GitHub App / GitLab OAuth, webhooks y builds (Dockerfile / Nixpacks) con deploy sin downtime
4. Previews por PR, rollback, bases de datos gestionadas, multi-servidor
