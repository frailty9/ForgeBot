.PHONY: fmt test build build-web run check

APP := forgebot

fmt:
	gofmt -w cmd internal

test:
	go test ./...

build-web:
	cd web && pnpm install --frozen-lockfile && pnpm build
	if not exist internal\server\adminui\dist mkdir internal\server\adminui\dist
	powershell -NoProfile -Command "Copy-Item -Recurse -Force web\dist\* internal\server\adminui\dist\"

build: build-web
	go build -o bin/$(APP) ./cmd/forgebot

run:
	go run ./cmd/forgebot -config configs/forgebot.example.json

check: fmt test build

