BIN=bin/epic-claim

.PHONY: build run test tidy docker

build:
	go build -o $(BIN) ./cmd/epic-claim

run:
	go run ./cmd/epic-claim -config config.yaml

test:
	go test ./...

tidy:
	go mod tidy

docker:
	docker compose build
