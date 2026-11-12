---
layout: ../../layouts/DocsLayout.astro
title: Users, roles and security
description: Invite your team with roles, turn on two-factor authentication and understand how Dockfly limits what Compose apps and the Docker socket can do.
---

## Users and roles

In *Settings → Users* you invite your team with a single-use link, valid for 7 days, that the administrator sends however they prefer. Each user has a role:

| Role | Can |
| --- | --- |
| Admin | Everything: proxy hosts, certificates, integrations, notifications, settings and users |
| Deployer | Sites, apps, builds and deployments, including their environment variables |
| Read-only | See the panel and the logs, without changing anything or seeing secrets |

If someone forgets their password, the administrator generates a link to reset it. Changing your password signs out the user's other sessions. There is always at least one administrator.

For access from scripts and CI, see [CLI and API tokens](/docs/cli/).

## Two-factor authentication

In *My account* (in the user menu) you turn on two-factor authentication with any TOTP app (Google Authenticator, 1Password, Bitwarden…). When you turn it on, Dockfly shows 10 single-use **recovery codes**.

If you lose both your phone and the codes, turn it off from the server:

```sh
docker exec dockfly dockfly reset-2fa you@email.com
```

## Protecting the panel

Mounting the Docker socket gives full control of the host. Protect port 81 with a firewall or a VPN, or publish the panel behind a [proxy host](/docs/proxy-hosts/) with SSL and close port 81.

If the panel and Traefik sit behind another proxy, such as Cloudflare, set `DOCKFLY_TRUSTED_PROXIES` so that the login attempt limit and access lists see the real client IP. See [Configuration](/docs/configuration/).

## Docker security

**Compose** apps are deployed by a helper container that has the Docker CLI. That helper does not receive the Docker socket: it talks to a **least-privilege proxy** inside Dockfly, over an internal network created for each deployment. The proxy rejects anything that would give control of the server:

- `privileged` containers, `cap_add`, `devices` and GPUs, `network_mode: host` and host namespaces;
- mounting sensitive host paths (`/`, `/etc`, `/home`, `/root`, sockets…) or Dockfly's own data;
- touching containers, volumes or networks of other apps or of Dockfly itself, and `prune` commands.

If a Compose file needs one of these, Compose shows the reason in the deployment log.

### Running behind docker-socket-proxy

To avoid giving Dockfly the full socket, it can run behind [docker-socket-proxy](https://github.com/Tecnativa/docker-socket-proxy):

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

With docker-socket-proxy, `build:` entries in Compose files do not work: BuildKit needs a channel (`/grpc`) that its HAProxy does not forward. Apps built from a Dockerfile and prebuilt images work the same.

## Builds

Site builds run in unprivileged, short-lived containers with resource limits, and repository credentials never enter them. By default their outbound traffic is filtered too. See [Builds](/docs/git-deploys/#builds) and `DOCKFLY_BUILD_EGRESS` in [Configuration](/docs/configuration/).
