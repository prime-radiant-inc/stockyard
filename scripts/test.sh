#!/bin/bash
# Run stockyard tests

set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(dirname "$SCRIPT_DIR")"

cd "$PROJECT_ROOT"

echo "=== Building ==="
go build -o bin/stockyard ./cmd/stockyard
go build -o bin/stockyardd ./cmd/stockyardd

echo ""
echo "=== Unit Tests ==="
go test ./pkg/... -v

echo ""
echo "=== All Tests Passed ==="
