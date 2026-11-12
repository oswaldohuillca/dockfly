---
layout: ../../layouts/DocsLayout.astro
title: Deploying from GitHub and GitLab
description: Connect a repository so every push builds and publishes your site, with branch previews, commit statuses and deploy hooks.
---

Connect a repository and every push to the branch you choose builds the site and publishes it.

## Connecting an account

- **GitHub**: in *Integrations → Connect with GitHub*, GitHub creates the "Dockfly" app of your installation in one click, already configured, and lets you choose the account or organisation and the repositories. Dockfly only asks for read access to the code, plus write access to commit statuses to mark each build as passed or failed, and uses tokens that expire every hour. GitHub Enterprise works too.
- **GitLab**: the OAuth application is registered once in GitLab; the panel shows the steps and the Redirect URI. After that, connecting an account is a click on "Authorize". It works with gitlab.com and with self-managed instances.
- **Personal access tokens**: an advanced option for environments without a browser.

## Deploying on every push

For a push to trigger a deployment, GitHub or GitLab must be able to reach Dockfly: publish the panel with a [proxy host](/docs/proxy-hosts/) and enter its address in *Integrations → Dockfly public URL*. Without it, sites are deployed with the **Deploy now** button.

## Builds

Builds run in a short-lived container (`oven/bun:1` by default, configurable per site), **without privileges** and with limits on memory, CPU, processes and time. Dockfly downloads the code and copies it into the container: **credentials never enter the build container**.

Each site has a **dependency cache** (bun, npm, yarn, pnpm) that is kept between builds. In the project's own test, `bun install` went from about 6 s to about 40 ms on the second build.

The limits, the number of simultaneous builds and the network access of builds are set with environment variables: see [Configuration](/docs/configuration/).

## Commit statuses

Each build marks its commit with the result (a check or a cross on the commit and on its pull requests), with a link to the published site or, if it failed, to the panel.

GitHub Apps created with earlier versions of Dockfly do not have that permission. Accept it in *GitHub → Settings → Applications → Dockfly → Permissions*. Builds work the same without it.

## Branch previews

Set a **preview domain** in *Settings → Branch previews* (for example `previews.mydomain.com`) and turn previews on for the site. From then on, every push to a branch other than the production branch is published at `branch--site.previews.mydomain.com` without touching production, and is removed when the branch is deleted, for example when the pull request is merged.

- One DNS record, `*.previews.mydomain.com`, pointing at the server is enough.
- HTTPS needs a wildcard certificate: configure the DNS zone in *Settings → Certificates* or upload your own certificate.
- Previews can be protected with an access list.
- Pull requests from forks are not built: their code is not trusted.

## Deploy hooks

A site linked to a repository can have a **deploy hook**: a secret URL that rebuilds the site's branch when it receives a `POST`. Paste it as a webhook in your CMS (Strapi, Contentful…) or call it from a cron job to rebuild when the content changes.

- Create it in the site's Repository card, where you can also copy, regenerate and delete it.
- The URL only rebuilds that one site. Regenerating it invalidates the previous URL.
- Anyone with the URL can trigger a build, so keep it private. Seeing it in the panel requires the deployer role.
- Builds started this way are labelled "Deploy hook".
