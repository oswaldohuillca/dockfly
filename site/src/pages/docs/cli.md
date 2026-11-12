---
layout: ../../layouts/DocsLayout.astro
title: CLI and API tokens
description: Create API tokens with a role and an expiry date, and deploy static sites from your terminal or from CI with the dockfly binary.
---

## API tokens

In *My account → API tokens* you create tokens to deploy from the terminal or from continuous integration.

- Each token has a role, never higher than its user's role. Roles are described in [Users, roles and security](/docs/users-and-security/).
- Each token has an expiry date and can be revoked at any time.
- A token cannot change the password, change two-factor authentication or create other tokens.

## The CLI

The same `dockfly` binary is the CLI. Set the address of your panel and a token, then deploy a build folder to a [static site](/docs/static-sites/):

```sh
export DOCKFLY_URL=https://dockfly.mydomain.com DOCKFLY_TOKEN=dfy_…
dockfly deploy ./dist --site my-site
```

Binaries for Linux, macOS and Windows are attached to each [release](https://github.com/oswaldohuillca/dockfly/releases).

## From CI

You can also use the Docker image instead of the binary. In a GitHub Actions workflow:

```yaml
# .github/workflows/deploy.yml
- run: npm ci && npm run build
- run: >
    docker run --rm -v "$PWD/dist:/dist" -e DOCKFLY_URL -e DOCKFLY_TOKEN
    ghcr.io/oswaldohuillca/dockfly deploy /dist --site my-site
  env:
    DOCKFLY_URL: https://dockfly.mydomain.com
    DOCKFLY_TOKEN: ${{ secrets.DOCKFLY_TOKEN }}
```

## Other commands

The binary inside the container also has maintenance commands, run with `docker exec`:

| Command | Purpose |
| --- | --- |
| `dockfly backup <file>` | Create a backup of Dockfly. See [Databases and backups](/docs/databases-and-backups/#backing-up-dockfly-itself) |
| `dockfly restore <file>` | Prepare a restore, applied on restart |
| `dockfly reset-2fa <email>` | Turn off [two-factor authentication](/docs/users-and-security/#two-factor-authentication) for a user |
| `dockfly agent` | Run as the agent of [another server](/docs/servers/) |
