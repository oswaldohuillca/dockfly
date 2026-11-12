---
layout: ../../layouts/DocsLayout.astro
title: Notifications
description: Get an alert by email, Discord, Slack, Telegram or webhook when a build fails, a server disconnects, resources run out or a scheduled task fails.
---

Dockfly sends alerts through notification channels. Managing them requires the admin role.

## Channels

A channel can be **email** (SMTP), **Discord**, **Slack**, **Telegram** or a generic **webhook**. Each channel chooses which events it is notified about, can be turned off without deleting it, and has a **Test** button that sends a test notification. Channel settings are stored encrypted, and secret fields are never shown again.

## Events

- A build failed, or succeeded.
- A certificate could not be issued, or is about to expire.
- A container went down, or recovered.
- A [database backup](/docs/databases-and-backups/#database-backups) failed.
- A [server](/docs/servers/) disconnected, or reconnected.
- **Disk, memory or CPU almost full**, and **back to normal**.
- **Scheduled task failed**.

### Disk, memory or CPU almost full

This alert is sent when:

| Resource | Threshold |
| --- | --- |
| Disk | 90% |
| Memory | 90% for 5 minutes in a row |
| CPU | 90% for 10 minutes in a row |

It applies to the panel's server and to connected remote servers. The **back to normal** event is sent when usage drops 5 points below the limit.

### Scheduled task failed

Sent when a [scheduled task](/docs/apps/#scheduled-tasks) of an app fails.
