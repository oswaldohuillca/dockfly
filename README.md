# Dockfly

A panel to publish applications behind **Traefik** with automatic SSL, in the style of
Nginx Proxy Manager. Everything ships in **a single image** (`scratch`, ~54 MB compressed) with
two binaries: `dockfly` (Go API + embedded React frontend) and `traefik`.

## Installation

All you need is Docker. Create a `docker-compose.yml`:

```yaml
services:
  dockfly:
    image: 'ghcr.io/oswaldohuillca/dockfly:latest'
    container_name: dockfly
    restart: unless-stopped

    ports:
      # Format <host-port>:<container-port>
      - '80:80'   # Public HTTP (Traefik)
      - '443:443' # Public HTTPS (Traefik)
      - '127.0.0.1:81:81' # Dockfly panel (local only; publish it over HTTPS, see below)
      # Add the ports of any TCP/UDP streams you want to expose
      # - '5432:5432' # PostgreSQL

    environment:
      # Docker network Dockfly shares with your apps (created if it doesn't exist).
      DOCKFLY_NETWORK: 'dockfly'

      # Email for Let's Encrypt (expiry notices). Optional.
      # DOCKFLY_ACME_EMAIL: 'you@email.com'

      # Uncomment to test against Let's Encrypt's staging environment
      # (no rate limits, but the certificates aren't trusted).
      # DOCKFLY_ACME_STAGING: 'true'

    volumes:
      # Database, certificates, published sites, backups…
      - ./data:/data
      # To manage containers and apps. Without it, only proxy hosts and sites.
      - /var/run/docker.sock:/var/run/docker.sock

    extra_hosts:
      # To point at services running directly on the server.
      - 'host.docker.internal:host-gateway'
```

```bash
docker compose up -d
```

The panel is published on `127.0.0.1:81` only, so it is never exposed over plain HTTP. For your
first visit, open an SSH tunnel from your machine:

```bash
ssh -L 8081:127.0.0.1:81 user@YOUR_SERVER
```

then go to `http://127.0.0.1:8081` to create the admin account (the first user to register is
the administrator). After that, publish the panel itself over HTTPS by creating a **proxy host**
pointing at `127.0.0.1:81`, and use that domain from then on.

**Updating.** **Settings → Dockfly version** shows a notice when a new version is available.
With the **Update** button, Dockfly copies its data to `/data/pre-update.tar.gz`, pulls the new
image and recreates itself with it (same volumes, ports, networks and variables). If the new
version doesn't come up healthy, it rolls back to the previous one. This works with the `:latest`
tag or with a pinned version (`:1.2.3` moves to the new one; change the tag in your
`docker-compose.yml` too, or a later `docker compose up` would bring the old version back). You
can also do it by hand:

```bash
docker compose pull && docker compose up -d
```

| Port | Use |
|---|---|
| 80 / 443 | Application traffic (Traefik) |
| 81 | Dockfly panel |

Images live at `ghcr.io/oswaldohuillca/dockfly` for **amd64 and arm64**: `:latest` is the most
recent release, `:1.2.3` a specific version and `:edge` the latest commit on `main`. Every
environment variable is listed under [Configuration](#configuration).

> ⚠️ The Docker socket grants full control of the server: protect port 81 (firewall or VPN), or
> publish the panel with a domain and SSL through a proxy host and close port 81.

To hack on Dockfly, the repository's `docker-compose.yml` builds the image from source:
`docker compose up -d --build`.

## Features

### Proxy host targets

- **Docker container**: pick the container from a list. If it doesn't share a network with
  Dockfly, it is connected to the `dockfly` network automatically.
- **This server**: apps outside Docker, via `host.docker.internal` (requires the compose
  `extra_hosts` entry; the app must listen on an address reachable from Docker).
- **External IP / URL**: any IP or domain. Turn off *Preserve the original Host* if the target
  expects its own domain (CDN, SaaS).

**Custom locations**: part of a domain goes to a different target, like in Nginx Proxy Manager.
For example, `app.mydomain.com` to the frontend and `app.mydomain.com/api` to the API container.
Each location has its own target, its own access list (the proxy host's, public, or another one)
and, if the service doesn't expect the prefix, the option to strip it (`/api/users` arrives as
`/users`). The longest location wins: `/api/v2` before `/api`.

### Static sites

Cloudflare Pages style: you create a site with its domains and upload your build folder
(`dist/`, `build/`…) or a `.zip` by dragging it onto the panel. Every upload is an **immutable
deployment** that goes live instantly; to **roll back**, publish an earlier one. The 10 most
recent are kept per site.

- Clean URLs: `/about` serves `about.html` or `about/index.html`.
- Custom `404.html`, or **SPA** mode (any path serves `index.html`).
- gzip/brotli compression, Let's Encrypt SSL and a per-deployment ETag.
- Dotfiles (`.env`, `.git`…) are never served, except `.well-known`.
- **`_redirects` and `_headers`** at the root of the build, in Cloudflare Pages format:

  ```
  # _redirects
  /old          /new           301
  /blog/*       /news/:splat   301
  /app/*        /app/index.html  200   # rewrite: same URL, different file
  ```
  ```
  # _headers
  /assets/*
    Cache-Control: public, max-age=31536000, immutable
  ```
  Redirects are applied before files; rewrites (200) only if the requested file doesn't exist.
- **Custom locations**, as in proxy hosts: part of the domain (`/api`) goes to a container or
  another target and the site serves the rest. That way a frontend and its API share a host (and
  cookies) with no CORS. The target receives the original `Host` and skips the site's
  compression; branch previews don't carry these locations.

The files are served by an internal Dockfly server (`127.0.0.1:8099`) that Traefik forwards each
site's domains to; no extra container needed.

### Deploying from GitHub and GitLab

Connect a repository and every push to the chosen branch builds the site and publishes it.

- **GitHub**: under *Integrations → Connect with GitHub*, GitHub creates this installation's
  "Dockfly" app in one click (already configured) and lets you choose the account or organisation
  and the repositories. Dockfly only asks for read access to the code (plus write access to
  commit statuses, to mark each build with ✓/✗) and uses tokens that expire every hour. It works
  with GitHub Enterprise too.
- **GitLab**: the OAuth application is registered once in GitLab (the panel walks you through the
  steps and the Redirect URI); after that, connecting an account is a single "Authorize" click.
  Works with gitlab.com and self-hosted instances.
- **Personal access tokens**: an advanced option, for environments without a browser.

Each site has a **dependency cache** (bun, npm, yarn, pnpm) that is kept between builds: in
testing, `bun install` went from ~6 s to ~40 ms on the second build.

Builds run in an ephemeral container (`oven/bun:1` by default, configurable per site)
**without privileges** and with memory, CPU, process and time limits. Dockfly downloads the code
and copies it in: **credentials never enter the build container**.

To deploy on every push, GitHub/GitLab must be able to reach Dockfly: publish the panel with a
proxy host and enter its address under *Integrations → Public URL*. Without it, sites are
deployed with the "Deploy now" button.

Each build marks its commit with the result (✓/✗ on the commit and on its pull requests), linking
to the published site or, if it failed, to the panel. GitHub Apps created with older Dockfly
versions don't have that permission: accept it under *GitHub → Settings → Applications →
Dockfly → Permissions* (builds work fine without it).

#### Branch previews

With a **preview domain** (*Settings → Branch previews*, e.g. `previews.mydomain.com`) and
previews enabled on the site, every push to a branch other than production is published at
`branch--site.previews.mydomain.com` without touching production, and is deleted when the branch
is deleted (when the pull request is merged, for instance). A single `*.previews.mydomain.com`
DNS record pointing at the server is enough. HTTPS needs a wildcard certificate: configure the
DNS zone under *Settings → Certificates* or upload your own certificate. Previews can be
protected with an access list. Pull requests from forks are not built: their code isn't trusted.

### Users and roles

Under *Settings → Users* you invite your team with a single-use link (valid for 7 days) that the
administrator sends however they like. Each user has a role:

| Role | Can |
| --- | --- |
| Administrator | Everything: proxy hosts, certificates, integrations, notifications, settings and users |
| Deployments | Sites, apps, builds and deployments, including their environment variables |
| Read only | View the panel and the logs, without changing anything or seeing secrets |

If someone forgets their password, the administrator generates a reset link for them. Changing
the password signs the user out everywhere else. There is always at least one administrator.

### API tokens and CLI

Under *My account → API tokens* you create tokens to deploy from a terminal or from CI. Each
token has a role (never higher than its user's) and an expiry, and can be revoked at any time.
The `dockfly` binary is also the CLI:

```sh
export DOCKFLY_URL=https://dockfly.mydomain.com DOCKFLY_TOKEN=dfy_…
dockfly deploy ./dist --site my-web
```

Binaries for Linux, macOS and Windows are attached to every release; you can also use the image:

```yaml
# .github/workflows/deploy.yml
- run: npm ci && npm run build
- run: >
    docker run --rm -v "$PWD/dist:/dist" -e DOCKFLY_URL -e DOCKFLY_TOKEN
    ghcr.io/oswaldohuillca/dockfly deploy /dist --site my-web
  env:
    DOCKFLY_URL: https://dockfly.mydomain.com
    DOCKFLY_TOKEN: ${{ secrets.DOCKFLY_TOKEN }}
```

A token cannot change the password, manage two-step verification or create other tokens.

### Docker security

**Compose** apps are deployed with a helper container that carries the Docker CLI. That helper
does not get the Docker socket: it talks to a **least-privilege proxy** inside Dockfly, over an
internal network created per deployment, which rejects anything that would hand over control of
the server:

- `privileged` containers, `cap_add`, `devices`/GPUs, `network_mode: host` and host namespaces;
- mounting sensitive host paths (`/`, `/etc`, `/home`, `/root`, sockets…) or Dockfly's own data;
- touching containers, volumes or networks belonging to other apps (or to Dockfly itself), and
  the `prune` endpoints.

If a `docker-compose.yml` needs any of that, Compose shows the reason in the deployment log
(`Dockfly: privileged containers… are not allowed`).

To avoid giving Dockfly the full socket, it can sit behind
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
    image: ghcr.io/oswaldohuillca/dockfly
    environment:
      DOCKFLY_DOCKER_SOCKET: tcp://socket-proxy:2375
    # …without mounting /var/run/docker.sock
```

With docker-socket-proxy, `build:` entries in a `docker-compose.yml` don't work: BuildKit needs a
channel (`/grpc`) that its HAProxy doesn't forward. Apps with a Dockerfile and prebuilt images
work as usual.

### Two-step verification

Under *My account* (the user menu) you enable two-step verification with any TOTP app (Google
Authenticator, 1Password, Bitwarden…). When you turn it on, Dockfly shows 10 single-use
**recovery codes**. If you lose both your phone and the codes, disable it from the server:

```sh
docker exec dockfly dockfly reset-2fa you@email.com
```

### Traffic

Every proxy host, site and app shows its **requests** (by status: 2xx/3xx, 4xx, 5xx) and its
**latency** (p50 and p95) over the last hour, day or week; the proxy host list includes a
sparkline for the last 24 hours. The data comes from Traefik's metrics (which only listen on
`127.0.0.1` inside the container), is collected every minute and kept for 7 days.

### Multiple servers

Under *Servers → Add server*, Dockfly gives you a command to run on another machine with Docker:

```sh
docker run -d --name dockfly-agent --restart unless-stopped \
  -p 80:80 -p 443:443 \
  -v /var/run/docker.sock:/var/run/docker.sock -v dockfly-agent:/data \
  ghcr.io/oswaldohuillca/dockfly agent --join https://dockfly.mydomain.com --token dfa_…
```

The **agent** connects to the panel (no ports to open on the server, it works behind NAT) and
runs its own Traefik. When you create an app you choose which server it runs on: the panel
deploys it there through the agent's tunnel (Dockerfile builds, zero-downtime deployments, logs,
database backups…) and sends it its routes. Its domains must point at that server's IP, which
requests its own certificates. **If the panel goes down, apps on those servers keep serving**;
when it comes back, the agent reconnects by itself. A disconnected server raises a notification.

For now, proxy hosts, static sites and Compose apps run on the panel's own server.

### Backups

Under *Settings → Backups* you can download a backup with the database, the keys, the published
sites and the certificates, and restore it by uploading it (also from the first-run screen, to
migrate to another server). From a terminal:

```bash
docker exec dockfly dockfly backup /data/backup.tar.gz   # create
docker exec dockfly dockfly restore /data/backup.tar.gz  # stage; applied on restart
```

> Copying `dockfly.db` by hand is not enough: SQLite runs in WAL mode and recent changes live in
> `dockfly.db-wal`. Dockfly's backup uses `VACUUM INTO`, which is consistent.

### Apps

Containers that Dockfly deploys and manages: APIs, databases, services… from an **image**
(`postgres:17`, `ghcr.io/user/app`) or from a **Dockerfile** in a GitHub/GitLab repo, rebuilt on
every push. There are one-click templates for PostgreSQL, MySQL, Redis, n8n and Uptime Kuma.

- **Zero downtime**: the new container starts, passes its health check (HTTP path, the image's
  `HEALTHCHECK` or an open port) and only then receives traffic. If it fails, the previous
  version keeps serving and the log shows the container's output.
- **With volumes** (databases) the current version is stopped before the new one starts: two
  instances writing the same data would corrupt it.
- Apps find each other by name on the internal network (`postgres:5432`), without being exposed.
- Also from a **`docker-compose.yml`** in the repo: Dockfly runs it with a helper container that
  carries Compose and connects the web service to its network. Use named volumes: relative host
  paths (`./data:/data`) don't work.
- Encrypted environment variables, memory and CPU limits, live logs, restart, stop and rollback
  to an earlier version.
- **Resources**: the container's live CPU and memory (CPU across all of the server's cores). For
  a Compose app it measures the service that receives the traffic.
- **Console**: runs a one-off command in the container (a migration, inspecting a file) and shows
  its output and exit code. It is not a terminal: every command starts fresh. Limited to
  2 minutes and 256 KB of output; "deployments" and administrator roles.
- **Scheduled tasks**: commands that run in the container on a five-field cron expression in
  **UTC** (`0 3 * * *`, `*/15 * * * *`). They can be paused and triggered by hand, and keep the
  output and exit code of their last run. Limited to 30 minutes; failures raise a notification.

Servers (the panel's own and any connected remote ones) raise a notification when **disk** usage
reaches 90 %, or **memory** or **CPU** stay at 90 % for 5 and 10 minutes, and again when they go
back to normal.

#### Database backups

**PostgreSQL**, **MySQL/MariaDB** and **Redis** apps have a *Backups* tab: compressed full
backups (`pg_dumpall`, `mysqldump`, `redis-cli --rdb`), scheduled hourly, daily or weekly,
keeping the last N, with a "Back up now" button. They can be downloaded and — for PostgreSQL and
MySQL — restored from the panel. Failed backups raise a notification.

They are stored on the server (`/data/db-backups`) or, better, in an **S3**-compatible bucket
(AWS, Cloudflare R2, Backblaze B2, MinIO…), configured under *Settings → Database backups*: that
way they survive losing the server. When you save the bucket, Dockfly checks that it can write
to it.

## Configuration

| Variable | Default | Description |
|---|---|---|
| `DOCKFLY_DATA_DIR` | `/data` | Database, session key, Traefik config and certificates |
| `DOCKFLY_DATABASE_URL` | — | The panel's database. Without it, SQLite in the data directory. See [The panel's database](#the-panels-database) |
| `DOCKFLY_LISTEN` | `:81` | Panel address |
| `DOCKFLY_ACME_EMAIL` | — | Email for Let's Encrypt (optional) |
| `DOCKFLY_ACME_STAGING` | `false` | Use Let's Encrypt's staging environment |
| `DOCKFLY_NETWORK` | `dockfly` | Docker network shared with the apps |
| `DOCKFLY_DOCKER_SOCKET` | `/var/run/docker.sock` | Docker socket, or `tcp://host:port` behind a socket proxy |
| `DOCKFLY_COMPOSE_PROXY` | `1` | `0` disables the least-privilege proxy for Compose apps (not recommended) |
| `DOCKFLY_TRAEFIK` | `on` | `off` to not launch Traefik (development) |
| `DOCKFLY_TRUSTED_PROXIES` | — | CIDR ranges (comma separated) of the proxies sitting in front of the panel and Traefik (Cloudflare's, for instance). `X-Forwarded-For` is only accepted from them, so rate limiting and access lists see the real IP. Empty = directly exposed |
| `DOCKFLY_BUILD_TIMEOUT_MIN` | `15` | Maximum time for a build |
| `DOCKFLY_BUILD_MEMORY_MB` / `DOCKFLY_BUILD_CPUS` | `2048` / `2` | Resources for each build |
| `DOCKFLY_BUILD_CONCURRENCY` | `1` | Concurrent builds |
| `DOCKFLY_BUILD_EGRESS` | `filtered` | `filtered`: builds reach the internet only through an HTTP(S) proxy (the `dockfly-egress` container) that blocks the local network, the host and cloud metadata; connections that aren't HTTP(S) through the proxy (`git+ssh`, for instance) fail. `open`: unrestricted egress, as before |
| `DOCKFLY_MAX_UPLOAD_MB` | `512` | Maximum size of a static site upload |
| `DOCKFLY_STATIC_ADDR` | `127.0.0.1:8099` | Internal address of the static site server |
| `DOCKFLY_LOG_LEVEL` / `DOCKFLY_TRAEFIK_LOG_LEVEL` | `info` / `warn` | Log level |

## Architecture

```
cmd/dockfly        entry point: API, Traefik supervisor, healthcheck
internal/api       REST API (chi) + serves the frontend
internal/store     models and queries with Bun over SQLite, PostgreSQL or MySQL (pure Go, no CGO)
  └─ migrations/   versioned SQL migrations, embedded in the binary
internal/pages     static sites: safe uploads (zip/folder), deployments and server
internal/apps      apps: zero-downtime deployments, health, volumes and lifecycle
internal/builder   build queue and execution in ephemeral containers
internal/gitprovider  GitHub/GitLab APIs: repos, code, webhooks, GitHub App and OAuth
internal/gitauth   fresh credentials per connection (installation tokens, OAuth refresh)
internal/secrets   AES-GCM encryption of tokens and environment variables
internal/traefik   generates traefik.yml (static) and dockfly.yml (dynamic); supervises the process
internal/docker    minimal Docker socket client (no SDK)
web/               React 19 + TypeScript + Tailwind 4 + shadcn/ui frontend, embedded with go:embed
  └─ src/routes/   file-based TanStack Router routes (one folder = one URL segment)
```

The database is the single source of truth: on every change (and at startup)
`/data/traefik/dynamic/dockfly.yml` is regenerated atomically and Traefik hot-reloads it
(~200 ms) without restarting.

> ⚠️ Mounting the Docker socket grants full control of the host. Protect port 81 (firewall or
> VPN, or publish the panel behind a proxy host with SSL).

## The panel's database

Dockfly stores its state in **SQLite** (`dockfly.db`, in the data directory) with no
configuration. To use a separate server, set `DOCKFLY_DATABASE_URL` before the first start:

```yaml
environment:
  # PostgreSQL (add ?sslmode=disable if the server doesn't use TLS)
  DOCKFLY_DATABASE_URL: postgres://dockfly:secret@db.example.com:5432/dockfly
  # MySQL 8 or MariaDB 10.11+ (add ?tls=true to encrypt the connection)
  # DOCKFLY_DATABASE_URL: mysql://dockfly:secret@db.example.com:3306/dockfly
```

The database must exist and be empty; Dockfly creates the tables at startup. Keep in mind:

- **Backups**: the panel's backup only carries the files (keys, sites, certificates). The
  database is backed up with `pg_dump` or `mysqldump`, and restoring from the panel is not
  available. Always keep the data directory together with the database: without `encryption.key`
  the stored secrets cannot be read.
- **Switching engines** does not migrate the data: an installation already on SQLite starts empty
  if you point it at `DOCKFLY_DATABASE_URL`.
- Don't use an app deployed by Dockfly itself as the panel's database: the panel needs it to
  start.

## Publishing a release

Every push to `main` publishes `ghcr.io/oswaldohuillca/dockfly:edge`. A tag publishes the
version, `:latest` and the CLI binaries on the GitHub release:

```bash
git tag v0.1.0 && git push origin v0.1.0
```

## Development

```bash
make dev                  # backend on :8181 without Traefik
cd web && npm run dev     # frontend with hot reload (proxies /api → :8181)
make test
```

The landing page lives in `site/` (Astro, static output): `cd site && bun install && bun run dev`.
`bun run build` leaves the site in `site/dist/`, ready for any static host.

### Languages

The panel is available in **English** (the default) and **Spanish**; each user switches it from
their own menu. Strings are written in English in the code with [Lingui](https://lingui.dev)
macros (`<Trans>…</Trans>`, ``t`…` ``) and their translations live in
`web/src/locales/<language>/messages.po`:

```bash
cd web && npm run extract   # adds new strings to the catalogues (and drops unused ones)
```

API errors are translated in the panel too: each one carries a stable code
(`{"error": "Wrong email or password", "code": "bad_credentials"}`) with the English message as a
fallback. The codes are defined in Go with `newError` and exported to
`web/src/lib/api-errors.gen.ts`:

```bash
go test ./internal/api -run TestAPIErrorsTS -update   # after adding or changing a code
cd web && npm run extract                              # then translate the new ones in the .po
```

The build fails if a language is missing any translation. To add another language, list it in
`web/lingui.config.ts` and `web/src/lib/i18n.ts`, run `npm run extract` and translate its `.po`.

### Contributing

Code, comments and commit messages are written in **English**. User-facing strings in the panel
are written in English too and translated through the `.po` catalogues described above.

## Roadmap

1. ✅ Proxy hosts with automatic SSL (Docker, host, external)
2. ✅ Static sites with direct uploads, immutable deployments and rollback
3. ✅ Static sites from GitHub/GitLab: build on every push
4. ✅ Containerised apps from an image or a Dockerfile
5. ✅ Branch previews and deployment status on commits

## License

[Apache-2.0](LICENSE).
