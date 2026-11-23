# syntax=docker/dockerfile:1

# The build stages run on the architecture of the building machine
# ($BUILDPLATFORM): Go cross-compiles and the frontend is the same everywhere.
# Only the final image targets the destination architecture, so building arm64
# on an amd64 machine needs no emulation.

# --- 1. Frontend ---
FROM --platform=$BUILDPLATFORM node:24-alpine AS web
WORKDIR /src/web
COPY web/package.json web/package-lock.json ./
RUN npm ci --no-audit --no-fund
COPY web/ ./
RUN npm run build

# --- 2. Backend (static binary with the frontend embedded) ---
FROM --platform=$BUILDPLATFORM golang:1.26-alpine AS api
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=web /src/web/dist ./web/dist
ARG VERSION=dev
ARG TARGETOS TARGETARCH
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -ldflags="-s -w -X main.version=${VERSION}" -o /out/dockfly ./cmd/dockfly

# --- 3. Traefik (we only take the official binary, for the target architecture) ---
FROM traefik:v3.7 AS traefik

# --- 4. Final image: scratch + two static binaries ---
FROM scratch
COPY --from=api /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=traefik /usr/local/bin/traefik /usr/local/bin/traefik
COPY --from=api /out/dockfly /usr/local/bin/dockfly

ENV DOCKFLY_DATA_DIR=/data
VOLUME /data
# 80/443: application traffic (Traefik) · 81: Dockfly panel
EXPOSE 80 443 81

HEALTHCHECK --interval=30s --timeout=5s --start-period=10s CMD ["/usr/local/bin/dockfly", "healthcheck"]
ENTRYPOINT ["/usr/local/bin/dockfly"]
