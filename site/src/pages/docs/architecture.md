---
layout: ../../layouts/DocsLayout.astro
title: Architecture
description: How Dockfly is built, a single image with a Go API, an embedded React panel and Traefik, with one database (SQLite by default) as the only source of truth.
---

Dockfly ships as a single image with two binaries: `dockfly`, the Go API with the React panel embedded, and `traefik`.

## Source of truth

The database is the only source of truth. On every change, and at startup, Dockfly regenerates `/data/traefik/dynamic/dockfly.yml` atomically and Traefik reloads it live, in about 200 ms, without restarting.

Static sites are served by an internal Dockfly server (`127.0.0.1:8099`) to which Traefik forwards each site's domains.

## Code layout

```
cmd/dockfly           entry point: API, Traefik supervisor, healthcheck
internal/api          REST API (chi) + serves the frontend
internal/store        models and queries with Bun on SQLite, PostgreSQL or MySQL (pure Go, no CGO)
  └─ migrations/      versioned SQL migrations, embedded in the binary
internal/pages        static sites: safe upload (zip/folder), deployments and server
internal/apps         apps: zero-downtime deployments, health, volumes and lifecycle
internal/builder      build queue and execution in short-lived containers
internal/gitprovider  GitHub/GitLab APIs: repos, code, webhooks, GitHub App and OAuth
internal/gitauth      valid credentials per connection (installation tokens, OAuth refresh)
internal/secrets      AES-GCM encryption of tokens and environment variables
internal/traefik      generates traefik.yml (static) and dockfly.yml (dynamic); supervises the process
internal/docker       minimal client for the Docker socket (no SDK)
web/                  React 19 + TypeScript + Tailwind 4 + shadcn/ui frontend, embedded with go:embed
  └─ src/routes/      file-based TanStack Router routes (one folder = one URL segment)
```

## Contributing

Dockfly is open source under the [Apache-2.0 license](https://github.com/oswaldohuillca/dockfly/blob/main/LICENSE). The development setup and the release process are described in the [repository's README](https://github.com/oswaldohuillca/dockfly#readme).
