VERSION ?= dev

.PHONY: web build dev test image up

web:
	cd web && npm ci && npm run build

build: web
	CGO_ENABLED=0 go build -trimpath -ldflags="-s -w -X main.version=$(VERSION)" -o bin/dockfly ./cmd/dockfly

# Backend en :8181 sin Traefik; el frontend se sirve con `cd web && npm run dev`.
dev:
	DOCKFLY_DATA_DIR=./data DOCKFLY_LISTEN=:8181 DOCKFLY_TRAEFIK=off go run ./cmd/dockfly

test:
	go test ./...

image:
	docker build --build-arg VERSION=$(VERSION) -t dockfly:$(VERSION) .

up:
	docker compose up -d --build
