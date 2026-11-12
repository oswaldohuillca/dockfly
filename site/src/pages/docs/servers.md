---
layout: ../../layouts/DocsLayout.astro
title: Multiple servers
description: Add more machines with one command. Each runs an agent with its own Traefik that connects out to the panel and keeps serving if the panel goes down.
---

## Adding a server

In *Servers → Add server*, Dockfly gives you a command to run on another machine that has Docker:

```sh
docker run -d --name dockfly-agent --restart unless-stopped \
  -p 80:80 -p 443:443 \
  -v /var/run/docker.sock:/var/run/docker.sock -v dockfly-agent:/data \
  ghcr.io/oswaldohuillca/dockfly agent --join https://dockfly.mydomain.com --token dfa_…
```

The panel must be reachable from the new server: publish it with a [proxy host](/docs/proxy-hosts/) and set its public URL in *Integrations*.

## How it works

The **agent** connects out to the panel, so there are no ports to open on the server for it and it works behind NAT. It runs its own Traefik, on the server's ports 80 and 443.

When you create an [app](/docs/apps/) you choose which server it runs on. The panel deploys it there through the agent's tunnel (Dockerfile builds, zero-downtime deployments, logs, database backups and so on) and sends the agent its routes.

- The app's domains must point at the IP of that server, which requests its own certificates.
- **If the panel goes down, the apps on the other servers keep serving.** When the panel comes back, the agent reconnects by itself.
- A disconnected server sends a [notification](/docs/notifications/).

## Current limits

For now, proxy hosts, static sites and Compose apps run on the panel's server.
