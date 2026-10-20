# syntax=docker/dockerfile:1

# --- 1. Frontend ---
FROM node:24-alpine AS web
WORKDIR /src/web
COPY web/package.json web/package-lock.json ./
RUN npm ci --no-audit --no-fund
COPY web/ ./
RUN npm run build

# --- 2. Backend (binario estático con el frontend incrustado) ---
FROM golang:1.26-alpine AS api
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=web /src/web/dist ./web/dist
ARG VERSION=dev
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w -X main.version=${VERSION}" -o /out/dockfly ./cmd/dockfly

# --- 3. Traefik (solo tomamos el binario oficial) ---
FROM traefik:v3.7 AS traefik

# --- 4. Imagen final: scratch + dos binarios estáticos ---
FROM scratch
COPY --from=api /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=traefik /usr/local/bin/traefik /usr/local/bin/traefik
COPY --from=api /out/dockfly /usr/local/bin/dockfly

ENV DOCKFLY_DATA_DIR=/data
VOLUME /data
# 80/443: tráfico de las apps (Traefik) · 81: panel de Dockfly
EXPOSE 80 443 81

HEALTHCHECK --interval=30s --timeout=5s --start-period=10s CMD ["/usr/local/bin/dockfly", "healthcheck"]
ENTRYPOINT ["/usr/local/bin/dockfly"]
