.PHONY: all build build-shell proto clean test test-unit lint fmt install uninstall
.PHONY: deploy deploy-all deploy-daemon deploy-image

all: proto build

build:
	go build -o bin/stockyard ./cmd/stockyard
	go build -o bin/stockyardd ./cmd/stockyardd

# Build VM binaries (static Linux binaries injected into rootfs at deploy time)
build-vm:
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o bin/stockyard-shell ./cmd/stockyard-shell
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -buildvcs=false -o bin/stockyard-snapshot ./vm-image/scripts/stockyard-snapshot

# Alias for backwards compat
build-shell: build-vm

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

# Deployment targets:
#   deploy        - Full deployment (daemon + VM image) - the default
#   deploy-all    - Alias for deploy
#   deploy-daemon - Build and install daemon binaries only
#   deploy-image  - Build and install VM image only (calls vm-image/Makefile)

# Full deployment: daemon binaries + VM image
# This is what you usually want - deploys everything
deploy: deploy-daemon deploy-image
	@echo ""
	@echo "=== Full deployment complete (daemon + VM image) ==="

# Alias for clarity
deploy-all: deploy

# Deploy daemon binaries only (build, install, restart)
deploy-daemon: build
	@echo ""
	@echo "=== Deploying daemon binaries ==="
	@echo ""
	@echo "Stopping stockyardd..."
	sudo systemctl stop stockyardd || true
	@echo "Installing binaries..."
	sudo install -m 755 bin/stockyard /usr/local/bin/stockyard
	sudo install -m 755 bin/stockyardd /usr/local/bin/stockyardd
	@echo "Starting stockyardd..."
	sudo systemctl start stockyardd
	@sleep 2
	@echo "Verifying daemon status..."
	@systemctl is-active stockyardd && echo "Daemon deploy successful!" || (echo "Deploy failed - daemon not running" && exit 1)

# Deploy VM image only (build + install via vm-image/Makefile)
# Note: This will also restart the daemon as part of the image deployment
deploy-image:
	@echo ""
	@echo "=== Deploying VM image ==="
	$(MAKE) -C vm-image deploy

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
