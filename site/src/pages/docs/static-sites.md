---
layout: ../../layouts/DocsLayout.astro
title: Static sites
description: Upload a build folder or a .zip and Dockfly publishes it as an immutable deployment, with rollback, redirects, headers and SPA mode.
---

Static sites work in the style of Cloudflare Pages: create a site with its domains and upload your build folder (`dist/`, `build/`…) or a `.zip` by dragging it onto the panel.

## Deployments and rollback

Every upload is an **immutable deployment** that is published instantly. To **roll back**, publish an earlier deployment. Dockfly keeps the 10 most recent deployments of each site.

To build and publish on every push instead of uploading by hand, see [Deploying from GitHub and GitLab](/docs/git-deploys/). To upload from a terminal or from CI, see the [CLI](/docs/cli/).

## How files are served

- Clean URLs: `/about` serves `about.html` or `about/index.html`.
- A custom `404.html`, or **SPA** mode, where any route serves `index.html`.
- gzip and brotli compression, Let's Encrypt SSL and one ETag per deployment.
- Files whose name starts with a dot (`.env`, `.git`…) are never served, except `.well-known`.

Files are served by an internal Dockfly server (`127.0.0.1:8099`) to which Traefik forwards each site's domains. No extra container is needed.

## Redirects and headers

Put a `_redirects` and a `_headers` file at the root of your build, in the Cloudflare Pages format:

```
# _redirects
/old          /new             301
/blog/*       /news/:splat     301
/app/*        /app/index.html  200   # rewrite: same URL, another file
```

```
# _headers
/assets/*
  Cache-Control: public, max-age=31536000, immutable
```

Redirects are applied before files. Rewrites (status 200) are applied only when the requested file does not exist.

## Custom locations

As with [proxy hosts](/docs/proxy-hosts/#custom-locations), part of the domain (`/api`, for example) can go to a container or another target while the site serves the rest. This lets a frontend and its API share a host, and its cookies, without CORS.

The target receives the original `Host` header and does not go through the site's compression. Branch previews do not include custom locations.
