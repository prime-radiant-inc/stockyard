package daemon

import (
	"bufio"
	"encoding/json"
	"os"
	"sync"
	"syscall"
	"time"

	"github.com/obra/stockyard/pkg/firecracker"
)

// MetricsCallback is called when new metrics are received.
type MetricsCallback func(*firecracker.FirecrackerMetrics)

// MetricsFIFOReader reads metrics from a Firecracker metrics FIFO.
type MetricsFIFOReader struct {
	path     string
	callback MetricsCallback
	stop     chan struct{}
	running  bool
	mu       sync.Mutex
	wg       sync.WaitGroup
}

// NewMetricsFIFOReader creates a new FIFO reader.
func NewMetricsFIFOReader(path string, callback MetricsCallback) *MetricsFIFOReader {
	return &MetricsFIFOReader{
		path:     path,
		callback: callback,
		stop:     make(chan struct{}),
	}
}

// Start begins reading from the FIFO.
func (r *MetricsFIFOReader) Start() error {
	r.mu.Lock()
	if r.running {
		r.mu.Unlock()
		return nil
	}
	r.running = true
	r.stop = make(chan struct{})
	r.wg.Add(1)
	r.mu.Unlock()

	go func() {
		defer r.wg.Done()
		r.readLoop()
	}()
	return nil
}

// Stop stops reading from the FIFO.
func (r *MetricsFIFOReader) Stop() {
	r.mu.Lock()
	if !r.running {
		r.mu.Unlock()
		return
	}
	close(r.stop)
	r.running = false
	r.mu.Unlock()
	r.wg.Wait() // Wait for readLoop to exit
}

func (r *MetricsFIFOReader) readLoop() {
	for {
		select {
		case <-r.stop:
			return
		default:
		}

		// Open FIFO non-blocking to allow checking stop signal
		file, err := os.OpenFile(r.path, os.O_RDONLY|syscall.O_NONBLOCK, 0)
		if err != nil {
			select {
			case <-r.stop:
				return
			case <-time.After(100 * time.Millisecond):
				continue
			}
		}

		// Clear non-blocking flag for normal reading
		fd := int(file.Fd())
		if err := syscall.SetNonblock(fd, false); err != nil {
			file.Close()
			continue
		}

		scanner := bufio.NewScanner(file)
		// Firecracker metrics JSON can exceed the default 64KB buffer
		scanner.Buffer(make([]byte, 1024*1024), 1024*1024)
		for scanner.Scan() {
			select {
			case <-r.stop:
				file.Close()
				return
			default:
			}

			var metrics firecracker.FirecrackerMetrics
			if err := json.Unmarshal(scanner.Bytes(), &metrics); err != nil {
				// Log parse errors but continue - metrics are best-effort
				continue
			}
			r.callback(&metrics)
		}
		file.Close()
	}
}
