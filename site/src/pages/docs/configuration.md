---
layout: ../../layouts/DocsLayout.astro
title: Configuration
description: Every environment variable Dockfly reads, with its default value, from the data directory and Let's Encrypt to build limits and trusted proxies.
---

Dockfly is configured with environment variables, set under `environment` in your `compose.yml`. All of them are optional.

<div class="table-scroll" tabindex="0" role="region" aria-label="Environment variables">

| Variable | Default | Description |
| --- | --- | --- |
| `DOCKFLY_DATA_DIR` | `/data` | Database, session key, and Traefik's configuration and certificates |
| `DOCKFLY_DATABASE_URL` | — | The panel's database. Without it, SQLite in the data folder. See [The panel's database](#the-panels-database) |
| `DOCKFLY_LISTEN` | `:81` | Address of the panel |
| `DOCKFLY_ACME_EMAIL` | — | Email for Let's Encrypt (optional) |
| `DOCKFLY_ACME_STAGING` | `false` | Use the Let's Encrypt staging environment |
| `DOCKFLY_NETWORK` | `dockfly` | Docker network shared with the apps |
| `DOCKFLY_DOCKER_SOCKET` | `/var/run/docker.sock` | Docker socket, or `tcp://host:port` behind a socket proxy |
| `DOCKFLY_COMPOSE_PROXY` | `1` | `0` turns off the least-privilege proxy of Compose apps (not recommended) |
| `DOCKFLY_TRAEFIK` | `on` | `off` to not start Traefik (development) |
| `DOCKFLY_TRUSTED_PROXIES` | — | Comma-separated CIDR ranges of the proxies in front of the panel and Traefik, for example Cloudflare's. `X-Forwarded-For` is accepted only from them, so the login attempt limit and access lists see the real IP. Empty means Dockfly is exposed directly |
| `DOCKFLY_BUILD_TIMEOUT_MIN` | `15` | Maximum duration of a build, in minutes |
| `DOCKFLY_BUILD_MEMORY_MB` / `DOCKFLY_BUILD_CPUS` | `2048` / `2` | Resources of each build |
| `DOCKFLY_BUILD_CONCURRENCY` | `1` | Simultaneous builds |
| `DOCKFLY_BUILD_EGRESS` | `filtered` | `filtered`: builds reach the internet only through an HTTP(S) proxy (the `dockfly-egress` container) that blocks the local network, the host and cloud metadata; connections that are not HTTP(S) through the proxy, such as `git+ssh`, fail. `open`: unrestricted outbound access |
| `DOCKFLY_MAX_UPLOAD_MB` | `512` | Maximum size of a static site upload |
| `DOCKFLY_STATIC_ADDR` | `127.0.0.1:8099` | Internal address of the static site server |
| `DOCKFLY_LOG_LEVEL` / `DOCKFLY_TRAEFIK_LOG_LEVEL` | `info` / `warn` | Log level |

</div>

## CLI variables

The `dockfly deploy` command reads `DOCKFLY_URL` and `DOCKFLY_TOKEN` on the machine where it runs. See [CLI and API tokens](/docs/cli/).

## The panel's database

Dockfly keeps its state in **SQLite** (`dockfly.db`, in the data folder) with nothing to configure. To use a separate server, set `DOCKFLY_DATABASE_URL` before the first start:

```yaml
environment:
  # PostgreSQL (add ?sslmode=disable if the server has no TLS)
  DOCKFLY_DATABASE_URL: postgres://dockfly:secret@db.example.com:5432/dockfly
  # MySQL 8 or MariaDB 10.11+ (add ?tls=true to encrypt the connection)
  # DOCKFLY_DATABASE_URL: mysql://dockfly:secret@db.example.com:3306/dockfly
```

The database must exist and be empty; Dockfly creates the tables when it starts. Keep in mind:

- **Backups.** The panel's backup then holds only the files (keys, sites, certificates). Back up the database with `pg_dump` or `mysqldump`; restoring from the panel is not available. Always keep the data folder together with the database: without `encryption.key` the stored secrets cannot be read.
- **Switching engines does not move your data.** An installation that already uses SQLite starts empty once you set `DOCKFLY_DATABASE_URL`.
- Do not use an app deployed by Dockfly itself as the panel's database: the panel needs it to start.
