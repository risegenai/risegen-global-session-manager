.PHONY: build test up down

build:
	go build ./...

test:
	go test ./...

vet:
	go vet ./...

up:
	docker compose up -d

down:
	docker compose down