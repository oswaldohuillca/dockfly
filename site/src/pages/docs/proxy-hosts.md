---
layout: ../../layouts/DocsLayout.astro
title: Proxy hosts
description: Point a domain at a Docker container, a service on the server or an external URL, with automatic SSL and a different target per path.
---

A proxy host sends the traffic of a domain to a target, with a certificate issued automatically.

## Targets

- **Docker container**: pick the container from a list. If it does not share a network with Dockfly, it is connected to the `dockfly` network automatically.
- **This server**: apps that run outside Docker, reached through `host.docker.internal`. This needs the `extra_hosts` entry from the [install file](/docs/), and the app must listen on an IP that Docker can reach.
- **IP / external URL**: any IP or domain. Turn off *Preserve the original Host* if the target requires its own domain (a CDN or a SaaS, for example).

## Custom locations

With custom locations, part of a domain goes to a different target, as in Nginx Proxy Manager. For example, `app.mydomain.com` goes to the frontend and `app.mydomain.com/api` goes to the API container.

Each location has:

- its own target;
- its own access list: the proxy host's, public, or a different one;
- an option to strip the prefix when the service does not expect it, so `/api/users` arrives as `/users`.

The longest location wins: `/api/v2` is matched before `/api`.

## The panel itself

To reach the Dockfly panel over HTTPS, create a proxy host that points at `127.0.0.1:81`. See [Getting started](/docs/#publish-the-panel-over-https).
