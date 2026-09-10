.PHONY: fmt test build run check

APP := forgebot

fmt:
	gofmt -w cmd internal

test:
	go test ./...

build:
	go build -o bin/$(APP) ./cmd/forgebot

run:
	go run ./cmd/forgebot -config configs/forgebot.example.json

check: fmt test build

