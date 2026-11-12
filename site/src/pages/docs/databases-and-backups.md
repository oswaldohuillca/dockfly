---
layout: ../../layouts/DocsLayout.astro
title: Databases and backups
description: Run PostgreSQL, MySQL or Redis as apps, schedule database backups to the server or an S3 bucket, and back up or migrate the whole panel.
---

## Databases

Databases are [apps](/docs/apps/): deploy one from an image such as `postgres:17` or from the one-click templates for PostgreSQL, MySQL and Redis.

- Other apps reach the database by its name on the internal network (`postgres:5432`), without exposing it.
- Because a database has volumes, the current version is stopped before a new one starts.
- To expose a database port outside the server, add the port to Dockfly's `compose.yml` (for example `'5432:5432'`), as shown in [Getting started](/docs/).

## Database backups

**PostgreSQL**, **MySQL/MariaDB** and **Redis** apps have a **Backups** tab:

- Full, compressed backups made with `pg_dumpall`, `mysqldump` and `redis-cli --rdb`.
- Scheduled every hour, day or week, keeping the last N.
- A **Back up now** button.
- Backups can be downloaded. PostgreSQL and MySQL backups can also be restored from the panel.
- Failed backups send a [notification](/docs/notifications/).

### Where backups are stored

Backups are stored on the server, in `/data/db-backups`, or, better, in an **S3**-compatible bucket (AWS, Cloudflare R2, Backblaze B2, MinIO…) configured in *Settings → Database backups*. In a bucket they survive even if the server is lost. When you save the bucket, Dockfly checks that it can write to it.

## Backing up Dockfly itself

*Settings → Backups* downloads a backup with the database, the keys, the published sites and the certificates. To restore it, upload it in the same place. You can also restore it from the first-install screen, which is how you move Dockfly to another server.

From the terminal:

```bash
docker exec dockfly dockfly backup /data/backup.tar.gz   # create
docker exec dockfly dockfly restore /data/backup.tar.gz  # prepare; applied on restart
```

> **Note.** Copying `dockfly.db` by hand is not enough: SQLite runs in WAL mode and recent changes live in `dockfly.db-wal`. Dockfly's backup uses `VACUUM INTO`, which is consistent.
