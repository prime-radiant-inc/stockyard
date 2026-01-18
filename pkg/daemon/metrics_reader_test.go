package daemon

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/obra/stockyard/pkg/firecracker"
)

func TestMetricsFIFOReader_ReadsMetrics(t *testing.T) {
	tmpDir := t.TempDir()
	fifoPath := filepath.Join(tmpDir, "metrics.fifo")

	if err := syscall.Mkfifo(fifoPath, 0644); err != nil {
		t.Fatalf("failed to create fifo: %v", err)
	}

	received := make(chan *firecracker.FirecrackerMetrics, 10)
	reader := NewMetricsFIFOReader(fifoPath, func(m *firecracker.FirecrackerMetrics) {
		received <- m
	})

	if err := reader.Start(); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer reader.Stop()

	// Write test metrics to FIFO
	go func() {
		f, _ := os.OpenFile(fifoPath, os.O_WRONLY, 0)
		defer f.Close()
		f.WriteString(`{"utc_timestamp_ms":1234567890,"vcpu":{"exit_io_in":100,"exit_io_out":50},"net":{"rx_bytes":1024,"tx_bytes":512}}` + "\n")
	}()

	select {
	case m := <-received:
		if m.Net.RxBytes != 1024 {
			t.Errorf("expected rx_bytes 1024, got %d", m.Net.RxBytes)
		}
	case <-time.After(2 * time.Second):
		t.Error("timeout waiting for metrics")
	}
}
