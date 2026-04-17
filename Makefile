.PHONY: build run migrate test lint tidy sqlc docker

GO = go
LDFLAGS = -trimpath -ldflags="-s -w"

build:
	$(GO) build $(LDFLAGS) -o bin/se8 ./cmd/se8
	$(GO) build $(LDFLAGS) -o bin/migrate ./cmd/migrate

build-linux-arm64:
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 $(GO) build $(LDFLAGS) -o bin/se8-linux-arm64 ./cmd/se8

build-linux-amd64:
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 $(GO) build $(LDFLAGS) -o bin/se8-linux-amd64 ./cmd/se8

run: build
	./bin/se8

test:
	$(GO) test -race -cover ./...

test-coverage:
	$(GO) test -race -coverprofile=coverage.out ./...
	$(GO) tool cover -func=coverage.out | tail -1

lint:
	golangci-lint run

sqlc:
	sqlc generate -f internal/storage/sqlc.yaml

tidy:
	$(GO) mod tidy

docker:
	docker build -t se8:local .
