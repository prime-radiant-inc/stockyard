.PHONY: all build build-client build-server build-guest proto clean test test-unit lint fmt container-image

all: proto build

build: build-server build-guest

# Client (CLI only).
# CGO_ENABLED=0 throughout: every binary ships as a static, pure-Go build
# (the daemon's SQLite state store uses the pure-Go modernc driver). Building
# and testing this way locally and in CI catches any accidental cgo dependency
# before it reaches a CGO_ENABLED=0 release tarball.
build-client:
	CGO_ENABLED=0 go build -o bin/stockyard ./cmd/stockyard

# Server (daemon + CLI)
build-server: build-client
	CGO_ENABLED=0 go build -o bin/stockyardd ./cmd/stockyardd

# Guest binaries (static Linux, run inside VMs)
build-guest: build-guest-amd64 build-guest-arm64

build-guest-amd64:
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o bin/stockyard-shell ./cmd/stockyard-shell
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o bin/stockyard-snapshot ./cmd/stockyard-snapshot

build-guest-arm64:
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -o bin/stockyard-shell-arm64 ./cmd/stockyard-shell
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -o bin/stockyard-snapshot-arm64 ./cmd/stockyard-snapshot


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
	CGO_ENABLED=0 go test ./pkg/... -v

# Development helpers
dev-daemon: build
	./bin/stockyardd

lint:
	golangci-lint run

fmt:
	go fmt ./...

container-image:
	$(MAKE) -C vm-image container-image

.DEFAULT_GOAL := build
