---
layout: ../../layouts/DocsLayout.astro
title: Apps
description: Deploy containers from an image, a Dockerfile or a Compose file with zero downtime, then roll back, watch resources, run commands and schedule tasks.
---

Apps are containers that Dockfly deploys and manages: APIs, databases, services and so on.

## Sources

- An **image**, such as `postgres:17` or `ghcr.io/user/app`.
- A **Dockerfile** in a GitHub or GitLab repository, rebuilt on every push. Connecting the repository is described in [Deploying from GitHub and GitLab](/docs/git-deploys/).
- A **`docker-compose.yml`** in the repository: see [Compose apps](#compose-apps) below.
- A one-click **template**: PostgreSQL, MySQL, Redis, n8n and Uptime Kuma.

If you have [more than one server](/docs/servers/), you choose which one the app runs on when you create it.

## Deployments

- **Zero downtime**: the new container starts, passes its health check (an HTTP path, the image's `HEALTHCHECK` or an open port) and only then receives traffic. If it fails, the previous version keeps serving and the log shows the container's output.
- **With volumes** (databases, for example), the current version is stopped before the new one starts: two instances writing to the same data would corrupt it.

Apps find each other by name on the internal network (`postgres:5432`) without being exposed.

Each app also has encrypted environment variables, memory and CPU limits, live logs, and restart and stop actions.

## Roll back

In an app's **Deployments** tab, **Roll back to this version** redeploys the image of a previous deployment without rebuilding it. This is only possible while that image still exists.

## Compose apps

Dockfly runs a `docker-compose.yml` from your repository with a helper container that has Compose, and connects the web service to its network.

Use named volumes: relative host paths such as `./data:/data` do not work.

Compose deployments go through a proxy that rejects anything that would give control of the server, such as privileged containers or sensitive host mounts. The full list is in [Docker security](/docs/users-and-security/#docker-security).

## Resources

The **Resources** tab shows the live CPU and memory of the app's container, refreshed every 5 seconds. CPU is shown as a share of all the server's cores. For Compose apps it measures only the service that receives the web traffic.

## Console

The **Console** tab runs a single command with `sh -c` inside the app's container and shows its output and exit code. It is available to the deployer and admin roles.

It is not an interactive terminal: every command starts from scratch, so `cd` and variables do not carry over from one command to the next.

Limits: 2 minutes per command, and output is cut at 256 KB.

## Scheduled tasks

The **Scheduled tasks** tab holds commands that run inside the app's container on a cron schedule.

The schedule has 5 fields and is evaluated in **UTC**:

```
minute  hour  day-of-month  month  day-of-week
```

| Syntax | Meaning | Example |
| --- | --- | --- |
| `*` | Every value | `* * * * *` |
| A value | That value only | `0 3 * * *` |
| `1-5` | A range | `0 9 * * 1-5` |
| `1,15` | A list | `0 0 1,15 * *` |
| `*/10` | A step | `*/10 * * * *` |

A task can be paused and can be run on demand with **Run now**. It shows the output and exit code of its last run.

- Tasks of stopped or undeployed apps are skipped.
- Each run is limited to 30 minutes.
- A failed task can send a [notification](/docs/notifications/).

## Database backups

PostgreSQL, MySQL/MariaDB and Redis apps have a Backups tab with scheduled backups. See [Databases and backups](/docs/databases-and-backups/).
