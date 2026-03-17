.PHONY: all build build-host build-guest build-shell proto clean test test-unit lint fmt

all: proto build

build: build-host build-guest

# Host binaries (daemon + CLI)
build-host:
	go build -o bin/stockyardd ./cmd/stockyardd
	go build -o bin/stockyard ./cmd/stockyard

# Guest binaries (static Linux, run inside Firecracker VMs)
build-guest:
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o bin/stockyard-shell ./cmd/stockyard-shell

# Alias for backwards compat
build-shell: build-guest

proto:
	mkdir -p pkg/api/v1
	protoc --go_out=. --go_opt=paths=source_relative \
		--go-grpc_out=. --go-grpc_opt=paths=source_relative \
		api/stockyard.proto
	mv api/*.go pkg/api/v1/

clean:
	rm -rf bin/

test: test-unit

test-unit:
	go test ./pkg/... -v

# Development helpers
dev-daemon: build
	./bin/stockyardd

lint:
	golangci-lint run

fmt:
	go fmt ./...

.DEFAULT_GOAL := build
