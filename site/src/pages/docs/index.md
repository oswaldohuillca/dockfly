---
layout: ../../layouts/DocsLayout.astro
title: Getting started
description: Install Dockfly with Docker, create the admin account through an SSH tunnel and publish the panel over HTTPS.
---

Dockfly is a panel to publish applications behind **Traefik** with automatic SSL, in the style of Nginx Proxy Manager. Everything ships in **a single image** (built on `scratch`, about 54 MB compressed) with two binaries: `dockfly` (the Go API with the React panel embedded) and `traefik`.

## Install

All you need is Docker. Save this `compose.yml` on your server:

```yaml
services:
  dockfly:
    image: 'ghcr.io/oswaldohuillca/dockfly:latest'
    container_name: dockfly
    restart: unless-stopped
    ports:
      - '80:80'
      - '443:443'
      - '127.0.0.1:81:81'
    environment:
      DOCKFLY_NETWORK: 'dockfly'
    volumes:
      - ./data:/data
      - /var/run/docker.sock:/var/run/docker.sock
    extra_hosts:
      - 'host.docker.internal:host-gateway'
```

Then start it:

```bash
docker compose up -d
```

What each part does:

- **Ports** use the format `<host port>:<container port>`. `80` and `443` are the public HTTP and HTTPS ports, served by Traefik. `81` is the Dockfly panel, published only on `127.0.0.1`. Add the ports of any TCP/UDP streams you want to expose, for example `'5432:5432'` for PostgreSQL.
- **`DOCKFLY_NETWORK`** is the Docker network Dockfly shares with your apps. Dockfly creates it if it does not exist.
- **`./data:/data`** holds the database, certificates, published sites, backups and the rest of Dockfly's data.
- **`/var/run/docker.sock`** lets Dockfly manage containers and apps. Without it you only get proxy hosts and static sites.
- **`extra_hosts`** lets a proxy host point at services that run directly on the server.

Two optional environment variables are worth knowing from the start: `DOCKFLY_ACME_EMAIL` sets the email Let's Encrypt uses for expiry notices, and `DOCKFLY_ACME_STAGING: 'true'` switches to the Let's Encrypt staging environment, which has no rate limits but issues certificates that browsers do not trust. Every variable is listed in [Configuration](/docs/configuration/).

| Port | Use |
| --- | --- |
| 80 / 443 | Traffic of your applications (Traefik) |
| 81 | Dockfly panel |

## First login

The panel is published only on `127.0.0.1:81` so that it is never exposed over plain HTTP. For the first login, open an SSH tunnel from your machine:

```bash
ssh -L 8081:127.0.0.1:81 user@your-server
```

Then open `http://127.0.0.1:8081` and create the administrator account. The first user to sign up is the administrator.

## Publish the panel over HTTPS

Once you are in, publish the panel itself: create a [proxy host](/docs/proxy-hosts/) that points at `127.0.0.1:81` and use that domain from then on.

> **Warning.** The Docker socket gives full control of the server. Protect port 81 with a firewall or a VPN, or publish the panel on a domain with SSL through a proxy host and close port 81.

## Images and tags

Images are published at `ghcr.io/oswaldohuillca/dockfly` for **amd64 and arm64**:

- `:latest` is the latest released version.
- `:1.2.3` is a specific version.
- `:edge` is the latest build of the `main` branch.

## Updating

**Settings → Dockfly version** shows a notice when a new version is available. The **Update** button makes Dockfly copy its data to `/data/pre-update.tar.gz`, pull the new image and recreate itself with it, keeping the same volumes, ports, networks and environment variables. If the new version does not come up healthy, Dockfly goes back to the previous one.

This works with the `:latest` tag and with a pinned version such as `:1.2.3`, which moves to the new version. If you pin a version, change the tag in your `compose.yml` too, or a later `docker compose up` would bring the old version back.

You can also update by hand:

```bash
docker compose pull && docker compose up -d
```
