VERSION ?= dev

.PHONY: web build dev dev-web dev-all test image up

web:
	cd web && npm ci && npm run build

build: web
	CGO_ENABLED=0 go build -trimpath -ldflags="-s -w -X main.version=$(VERSION)" -o bin/dockfly ./cmd/dockfly

# Backend on :8181 without Traefik; serve the frontend with `cd web && npm run dev`.
dev:
	DOCKFLY_DATA_DIR=./data DOCKFLY_LISTEN=:8181 DOCKFLY_TRAEFIK=off go run ./cmd/dockfly

dev-web:
	cd web && npm run dev

# Backend and frontend at once; Ctrl+C stops both.
dev-all:
	$(MAKE) -j2 dev dev-web

test:
	go test ./...

image:
	docker build --build-arg VERSION=$(VERSION) -t dockfly:$(VERSION) .

up:
	docker compose up -d --build
