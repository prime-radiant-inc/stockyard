.PHONY: all build proto clean test test-unit lint fmt install uninstall deploy

all: proto build

build:
	go build -o bin/stockyard ./cmd/stockyard
	go build -o bin/stockyardd ./cmd/stockyardd

proto:
	mkdir -p pkg/api/v1
	protoc --go_out=. --go_opt=paths=source_relative \
		--go-grpc_out=. --go-grpc_opt=paths=source_relative \
		api/stockyard.proto
	mv api/*.go pkg/api/v1/

clean:
	rm -rf bin/

install: build
	install -m 755 bin/stockyard /usr/local/bin/stockyard
	install -m 755 bin/stockyardd /usr/local/bin/stockyardd

uninstall:
	rm -f /usr/local/bin/stockyard /usr/local/bin/stockyardd

# Deploy: build, install, and restart the daemon
deploy: build
	@echo "Stopping stockyardd..."
	sudo systemctl stop stockyardd || true
	@echo "Installing binaries..."
	sudo install -m 755 bin/stockyard /usr/local/bin/stockyard
	sudo install -m 755 bin/stockyardd /usr/local/bin/stockyardd
	@echo "Starting stockyardd..."
	sudo systemctl start stockyardd
	@sleep 2
	@echo "Verifying daemon status..."
	@systemctl is-active stockyardd && echo "Deploy successful!" || (echo "Deploy failed - daemon not running" && exit 1)

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
