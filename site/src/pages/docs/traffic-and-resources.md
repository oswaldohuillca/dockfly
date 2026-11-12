---
layout: ../../layouts/DocsLayout.astro
title: Traffic and resources
description: See requests and latency for every proxy host, site and app, and the live CPU and memory of each app, with nothing extra to set up.
---

## Traffic

Every proxy host, site and app shows:

- its **requests**, by status: 2xx/3xx, 4xx and 5xx;
- its **latency**: p50 and p95.

You can look at the last hour, the last day or the last week. The list of proxy hosts also includes a small chart of the last 24 hours.

The data comes from Traefik's metrics, which only listen on `127.0.0.1` inside the container. It is collected every minute and kept for 7 days.

## App resources

Each app has a **Resources** tab with the live CPU and memory of its container, refreshed every 5 seconds. CPU is shown as a share of all the server's cores. For Compose apps it measures only the service that receives the web traffic. See [Apps](/docs/apps/#resources).

## Alerts

Dockfly can notify you when the disk, memory or CPU of the panel's server or of a connected remote server is almost full, and again when it is back to normal. The thresholds are in [Notifications](/docs/notifications/#disk-memory-or-cpu-almost-full).
