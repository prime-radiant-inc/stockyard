.PHONY: all build proto clean test test-unit lint fmt install uninstall

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
