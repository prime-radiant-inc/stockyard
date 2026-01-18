# Dashboard Phase 2: Real-time Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Add WebSocket infrastructure for live log streaming, metrics updates, and real-time VM status changes.

**Architecture:** WebSocket endpoints alongside REST API. Daemon collects metrics from Firecracker API and streams logs from VM stdout/stderr. Frontend uses htmx WebSocket extension for live updates.

**Tech Stack:** gorilla/websocket, Firecracker metrics API, htmx ws extension

**Prerequisites:** Phase 1 must be complete.

---

## Task 1: Add WebSocket Dependencies

**Files:**
- Modify: `/home/jesse/git/stockyard/go.mod`

**Step 1: Add gorilla/websocket**

Run:
```bash
go get github.com/gorilla/websocket@v1.5.1
```

**Step 2: Verify dependency added**

Run: `grep gorilla go.mod`
Expected: `github.com/gorilla/websocket v1.5.1`

**Step 3: Commit**

```bash
git add go.mod go.sum
git commit -m "deps: add gorilla/websocket for real-time updates"
```

---

## Task 2: Create WebSocket Hub

**Files:**
- Create: `/home/jesse/git/stockyard/pkg/dashboard/websocket.go`
- Create: `/home/jesse/git/stockyard/pkg/dashboard/websocket_test.go`

**Step 1: Write the failing test**

Create `pkg/dashboard/websocket_test.go`:

```go
package dashboard

import (
	"testing"
	"time"
)

func TestHub_BroadcastMessage(t *testing.T) {
	hub := NewHub()
	go hub.Run()
	defer hub.Stop()

	// Create a test client
	client := &Client{
		hub:      hub,
		taskID:   "task-1",
		send:     make(chan []byte, 256),
	}

	hub.Register(client)
	time.Sleep(10 * time.Millisecond) // Let register process

	// Broadcast a message
	hub.Broadcast("task-1", []byte("test message"))
	time.Sleep(10 * time.Millisecond) // Let broadcast process

	select {
	case msg := <-client.send:
		if string(msg) != "test message" {
			t.Errorf("expected 'test message', got '%s'", string(msg))
		}
	case <-time.After(100 * time.Millisecond):
		t.Error("expected message but got none")
	}
}

func TestHub_ClientUnsubscribe(t *testing.T) {
	hub := NewHub()
	go hub.Run()
	defer hub.Stop()

	client := &Client{
		hub:    hub,
		taskID: "task-1",
		send:   make(chan []byte, 256),
	}

	hub.Register(client)
	time.Sleep(10 * time.Millisecond)

	hub.Unregister(client)
	time.Sleep(10 * time.Millisecond)

	// Broadcast should not panic
	hub.Broadcast("task-1", []byte("test"))
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./pkg/dashboard/... -run TestHub -v`
Expected: FAIL - Hub undefined

**Step 3: Write minimal implementation**

Create `pkg/dashboard/websocket.go`:

```go
package dashboard

import (
	"sync"
)

// Client represents a WebSocket client connection.
type Client struct {
	hub    *Hub
	taskID string // Which task this client is subscribed to (empty = all)
	send   chan []byte
}

// Hub manages WebSocket client connections and message broadcasting.
type Hub struct {
	clients    map[*Client]bool
	broadcast  chan broadcastMsg
	register   chan *Client
	unregister chan *Client
	stop       chan struct{}
	mu         sync.RWMutex
}

type broadcastMsg struct {
	taskID string
	data   []byte
}

// NewHub creates a new WebSocket hub.
func NewHub() *Hub {
	return &Hub{
		clients:    make(map[*Client]bool),
		broadcast:  make(chan broadcastMsg, 256),
		register:   make(chan *Client),
		unregister: make(chan *Client),
		stop:       make(chan struct{}),
	}
}

// Run starts the hub's main loop.
func (h *Hub) Run() {
	for {
		select {
		case client := <-h.register:
			h.mu.Lock()
			h.clients[client] = true
			h.mu.Unlock()

		case client := <-h.unregister:
			h.mu.Lock()
			if _, ok := h.clients[client]; ok {
				delete(h.clients, client)
				close(client.send)
			}
			h.mu.Unlock()

		case msg := <-h.broadcast:
			h.mu.RLock()
			for client := range h.clients {
				// Send to clients subscribed to this task or to all tasks
				if client.taskID == "" || client.taskID == msg.taskID {
					select {
					case client.send <- msg.data:
					default:
						// Client buffer full, skip
					}
				}
			}
			h.mu.RUnlock()

		case <-h.stop:
			h.mu.Lock()
			for client := range h.clients {
				close(client.send)
				delete(h.clients, client)
			}
			h.mu.Unlock()
			return
		}
	}
}

// Stop shuts down the hub.
func (h *Hub) Stop() {
	close(h.stop)
}

// Register adds a client to the hub.
func (h *Hub) Register(client *Client) {
	h.register <- client
}

// Unregister removes a client from the hub.
func (h *Hub) Unregister(client *Client) {
	h.unregister <- client
}

// Broadcast sends a message to all clients subscribed to a task.
func (h *Hub) Broadcast(taskID string, data []byte) {
	select {
	case h.broadcast <- broadcastMsg{taskID: taskID, data: data}:
	default:
		// Broadcast buffer full, drop message
	}
}

// BroadcastAll sends a message to all connected clients.
func (h *Hub) BroadcastAll(data []byte) {
	h.Broadcast("", data)
}
```

**Step 4: Run test to verify it passes**

Run: `go test ./pkg/dashboard/... -run TestHub -v`
Expected: PASS

**Step 5: Commit**

```bash
git add pkg/dashboard/websocket.go pkg/dashboard/websocket_test.go
git commit -m "feat(dashboard): add WebSocket hub for message broadcasting"
```

---

## Task 3: Add WebSocket Handler

**Files:**
- Modify: `/home/jesse/git/stockyard/pkg/dashboard/websocket.go`
- Modify: `/home/jesse/git/stockyard/pkg/dashboard/websocket_test.go`

**Step 1: Write the failing test**

Add to `websocket_test.go`:

```go
import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestWebSocketHandler_Connect(t *testing.T) {
	hub := NewHub()
	go hub.Run()
	defer hub.Stop()

	handler := NewWebSocketHandler(hub)
	server := httptest.NewServer(handler)
	defer server.Close()

	// Convert http:// to ws://
	wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "?task=task-1"

	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("failed to connect: %v", err)
	}
	defer conn.Close()

	// Send a test message through the hub
	hub.Broadcast("task-1", []byte(`{"type":"test"}`))

	// Read the message
	conn.SetReadDeadline(time.Now().Add(time.Second))
	_, msg, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("failed to read message: %v", err)
	}

	if !strings.Contains(string(msg), "test") {
		t.Errorf("expected test message, got %s", string(msg))
	}
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./pkg/dashboard/... -run TestWebSocketHandler -v`
Expected: FAIL - NewWebSocketHandler undefined

**Step 3: Write minimal implementation**

Add to `websocket.go`:

```go
import (
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	CheckOrigin: func(r *http.Request) bool {
		return true // Allow all origins for now
	},
}

// WebSocketHandler handles WebSocket connections.
type WebSocketHandler struct {
	hub *Hub
}

// NewWebSocketHandler creates a new WebSocket handler.
func NewWebSocketHandler(hub *Hub) *WebSocketHandler {
	return &WebSocketHandler{hub: hub}
}

func (h *WebSocketHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("WebSocket upgrade error: %v", err)
		return
	}

	taskID := r.URL.Query().Get("task")

	client := &Client{
		hub:    h.hub,
		taskID: taskID,
		send:   make(chan []byte, 256),
	}

	h.hub.Register(client)

	// Start writer goroutine
	go h.writePump(conn, client)

	// Reader goroutine (handles client disconnect)
	go h.readPump(conn, client)
}

func (h *WebSocketHandler) writePump(conn *websocket.Conn, client *Client) {
	ticker := time.NewTicker(30 * time.Second)
	defer func() {
		ticker.Stop()
		conn.Close()
	}()

	for {
		select {
		case message, ok := <-client.send:
			if !ok {
				conn.WriteMessage(websocket.CloseMessage, []byte{})
				return
			}
			conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if err := conn.WriteMessage(websocket.TextMessage, message); err != nil {
				return
			}

		case <-ticker.C:
			conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if err := conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}

func (h *WebSocketHandler) readPump(conn *websocket.Conn, client *Client) {
	defer func() {
		h.hub.Unregister(client)
		conn.Close()
	}()

	conn.SetReadLimit(512)
	conn.SetReadDeadline(time.Now().Add(60 * time.Second))
	conn.SetPongHandler(func(string) error {
		conn.SetReadDeadline(time.Now().Add(60 * time.Second))
		return nil
	})

	for {
		_, _, err := conn.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
				log.Printf("WebSocket error: %v", err)
			}
			break
		}
	}
}
```

**Step 4: Run test to verify it passes**

Run: `go test ./pkg/dashboard/... -run TestWebSocketHandler -v`
Expected: PASS

**Step 5: Commit**

```bash
git add pkg/dashboard/websocket.go pkg/dashboard/websocket_test.go
git commit -m "feat(dashboard): add WebSocket handler with gorilla/websocket"
```

---

## Task 4: Integrate WebSocket Hub into Server

**Files:**
- Modify: `/home/jesse/git/stockyard/pkg/dashboard/server.go`
- Modify: `/home/jesse/git/stockyard/pkg/dashboard/server_test.go`

**Step 1: Write the failing test**

Add to `server_test.go`:

```go
func TestServer_HasWebSocketEndpoint(t *testing.T) {
	mock := &MockDaemon{}
	srv := NewServer(mock)

	// Just verify the route exists - actual WS testing done in websocket_test.go
	req := httptest.NewRequest("GET", "/ws", nil)
	w := httptest.NewRecorder()

	srv.ServeHTTP(w, req)

	// Should get "Bad Request" because we're not upgrading
	if w.Code == http.StatusNotFound {
		t.Error("expected /ws endpoint to exist")
	}
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./pkg/dashboard/... -run TestServer_HasWebSocketEndpoint -v`
Expected: FAIL - 404

**Step 3: Write minimal implementation**

Update `server.go`:

```go
// Server is the HTTP server for the web dashboard.
type Server struct {
	mux       *http.ServeMux
	daemon    DaemonAPI
	templates *template.Template
	hub       *Hub
}

// NewServer creates a new dashboard HTTP server.
func NewServer(daemon DaemonAPI) *Server {
	tmpl, err := LoadTemplates()
	if err != nil {
		log.Printf("Warning: failed to load templates: %v", err)
	}

	hub := NewHub()
	go hub.Run()

	s := &Server{
		mux:       http.NewServeMux(),
		daemon:    daemon,
		templates: tmpl,
		hub:       hub,
	}
	s.registerRoutes()
	return s
}

func (s *Server) registerRoutes() {
	s.mux.HandleFunc("/health", s.handleHealth)
	s.mux.HandleFunc("/ws", s.handleWebSocket)
	s.mux.HandleFunc("/api/vm/", s.handleAPIVM)
	s.mux.HandleFunc("/vm/", s.handleVMDetail)
	s.mux.HandleFunc("/", s.handleFleet)
}

func (s *Server) handleWebSocket(w http.ResponseWriter, r *http.Request) {
	handler := NewWebSocketHandler(s.hub)
	handler.ServeHTTP(w, r)
}

// Hub returns the WebSocket hub for external broadcasting.
func (s *Server) Hub() *Hub {
	return s.hub
}

// Close shuts down the server's resources.
func (s *Server) Close() {
	if s.hub != nil {
		s.hub.Stop()
	}
}
```

**Step 4: Run test to verify it passes**

Run: `go test ./pkg/dashboard/... -run TestServer_HasWebSocketEndpoint -v`
Expected: PASS

**Step 5: Commit**

```bash
git add pkg/dashboard/server.go pkg/dashboard/server_test.go
git commit -m "feat(dashboard): integrate WebSocket hub into server"
```

---

## Task 5: Create Log Streaming Infrastructure

**Files:**
- Create: `/home/jesse/git/stockyard/pkg/dashboard/logs.go`
- Create: `/home/jesse/git/stockyard/pkg/dashboard/logs_test.go`

**Step 1: Write the failing test**

Create `pkg/dashboard/logs_test.go`:

```go
package dashboard

import (
	"context"
	"testing"
	"time"
)

func TestLogStreamer_BroadcastsLogs(t *testing.T) {
	hub := NewHub()
	go hub.Run()
	defer hub.Stop()

	streamer := NewLogStreamer(hub)

	// Create a receiving client
	client := &Client{
		hub:    hub,
		taskID: "task-1",
		send:   make(chan []byte, 256),
	}
	hub.Register(client)
	time.Sleep(10 * time.Millisecond)

	// Send a log line
	streamer.SendLog("task-1", "stdout", "Hello from VM")

	select {
	case msg := <-client.send:
		if len(msg) == 0 {
			t.Error("expected log message")
		}
	case <-time.After(100 * time.Millisecond):
		t.Error("timeout waiting for log message")
	}
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./pkg/dashboard/... -run TestLogStreamer -v`
Expected: FAIL - LogStreamer undefined

**Step 3: Write minimal implementation**

Create `pkg/dashboard/logs.go`:

```go
package dashboard

import (
	"encoding/json"
	"time"
)

// LogEntry represents a single log line.
type LogEntry struct {
	Type      string    `json:"type"`      // "log"
	TaskID    string    `json:"task_id"`
	Stream    string    `json:"stream"`    // "stdout" or "stderr"
	Line      string    `json:"line"`
	Timestamp time.Time `json:"timestamp"`
}

// LogStreamer manages log streaming to WebSocket clients.
type LogStreamer struct {
	hub *Hub
}

// NewLogStreamer creates a new log streamer.
func NewLogStreamer(hub *Hub) *LogStreamer {
	return &LogStreamer{hub: hub}
}

// SendLog broadcasts a log line to subscribed clients.
func (l *LogStreamer) SendLog(taskID, stream, line string) {
	entry := LogEntry{
		Type:      "log",
		TaskID:    taskID,
		Stream:    stream,
		Line:      line,
		Timestamp: time.Now(),
	}

	data, err := json.Marshal(entry)
	if err != nil {
		return
	}

	l.hub.Broadcast(taskID, data)
}

// SendLogBatch broadcasts multiple log lines efficiently.
func (l *LogStreamer) SendLogBatch(taskID string, entries []LogEntry) {
	for _, entry := range entries {
		entry.Type = "log"
		entry.TaskID = taskID
		data, err := json.Marshal(entry)
		if err != nil {
			continue
		}
		l.hub.Broadcast(taskID, data)
	}
}
```

**Step 4: Run test to verify it passes**

Run: `go test ./pkg/dashboard/... -run TestLogStreamer -v`
Expected: PASS

**Step 5: Commit**

```bash
git add pkg/dashboard/logs.go pkg/dashboard/logs_test.go
git commit -m "feat(dashboard): add log streaming infrastructure"
```

---

## Task 5A: Add Log Tailer for VM Logs

**Files:**
- Create: `/home/jesse/git/stockyard/pkg/daemon/logtailer.go`
- Create: `/home/jesse/git/stockyard/pkg/daemon/logtailer_test.go`

**Context:** Firecracker writes stdout/stderr to `{vmDir}/stdout.log` and `{vmDir}/stderr.log`. We need to tail these files and broadcast logs via LogStreamer.

**Step 1: Write the failing test**

Create `pkg/daemon/logtailer_test.go`:

```go
package daemon

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

type mockLogSink struct {
	received []struct {
		taskID, stream, line string
	}
}

func (m *mockLogSink) SendLog(taskID, stream, line string) {
	m.received = append(m.received, struct {
		taskID, stream, line string
	}{taskID, stream, line})
}

func TestLogTailer_TailsLogFile(t *testing.T) {
	// Create temp directory and log file
	tmpDir := t.TempDir()
	logFile := filepath.Join(tmpDir, "stdout.log")
	os.WriteFile(logFile, []byte("line 1\nline 2\n"), 0644)

	sink := &mockLogSink{}
	tailer := NewLogTailer(sink)

	// Start tailing
	err := tailer.TailFile("task-1", "stdout", logFile)
	if err != nil {
		t.Fatalf("TailFile failed: %v", err)
	}
	defer tailer.Stop()

	// Wait for initial lines
	time.Sleep(100 * time.Millisecond)

	if len(sink.received) < 2 {
		t.Errorf("expected at least 2 lines, got %d", len(sink.received))
	}
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./pkg/daemon/... -run TestLogTailer -v`
Expected: FAIL - LogTailer undefined

**Step 3: Write minimal implementation**

Create `pkg/daemon/logtailer.go`:

```go
package daemon

import (
	"bufio"
	"os"
	"sync"
)

// LogSink receives log lines from the tailer.
type LogSink interface {
	SendLog(taskID, stream, line string)
}

// LogTailer tails VM log files and sends lines to a sink.
type LogTailer struct {
	sink    LogSink
	tailers map[string]chan struct{}
	mu      sync.Mutex
}

// NewLogTailer creates a new log tailer.
func NewLogTailer(sink LogSink) *LogTailer {
	return &LogTailer{
		sink:    sink,
		tailers: make(map[string]chan struct{}),
	}
}

// TailFile starts tailing a log file for a task.
func (t *LogTailer) TailFile(taskID, stream, path string) error {
	key := taskID + ":" + stream

	t.mu.Lock()
	if _, exists := t.tailers[key]; exists {
		t.mu.Unlock()
		return nil // Already tailing
	}
	stop := make(chan struct{})
	t.tailers[key] = stop
	t.mu.Unlock()

	go t.tailLoop(taskID, stream, path, stop)
	return nil
}

// StopTask stops tailing all files for a task.
func (t *LogTailer) StopTask(taskID string) {
	t.mu.Lock()
	defer t.mu.Unlock()

	for key, stop := range t.tailers {
		if len(key) > len(taskID) && key[:len(taskID)+1] == taskID+":" {
			close(stop)
			delete(t.tailers, key)
		}
	}
}

// Stop stops all tailers.
func (t *LogTailer) Stop() {
	t.mu.Lock()
	defer t.mu.Unlock()

	for _, stop := range t.tailers {
		close(stop)
	}
	t.tailers = make(map[string]chan struct{})
}

func (t *LogTailer) tailLoop(taskID, stream, path string, stop chan struct{}) {
	file, err := os.Open(path)
	if err != nil {
		return
	}
	defer file.Close()

	// Read existing content first
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		select {
		case <-stop:
			return
		default:
			t.sink.SendLog(taskID, stream, scanner.Text())
		}
	}

	// Then tail for new content
	for {
		select {
		case <-stop:
			return
		default:
			if scanner.Scan() {
				t.sink.SendLog(taskID, stream, scanner.Text())
			} else {
				// Wait for more content
				time.Sleep(100 * time.Millisecond)
				// Re-check file (scanner resets on next Scan if EOF was temporary)
			}
		}
	}
}
```

Note: Add `"time"` to imports.

**Step 4: Run test to verify it passes**

Run: `go test ./pkg/daemon/... -run TestLogTailer -v`
Expected: PASS

**Step 5: Commit**

```bash
git add pkg/daemon/logtailer.go pkg/daemon/logtailer_test.go
git commit -m "feat(daemon): add log tailer for VM log files"
```

---

## Task 6: Create Metrics Streaming Infrastructure

**Files:**
- Create: `/home/jesse/git/stockyard/pkg/dashboard/metrics.go`
- Create: `/home/jesse/git/stockyard/pkg/dashboard/metrics_test.go`

**Step 1: Write the failing test**

Create `pkg/dashboard/metrics_test.go`:

```go
package dashboard

import (
	"testing"
	"time"
)

func TestMetricsCollector_BroadcastsMetrics(t *testing.T) {
	hub := NewHub()
	go hub.Run()
	defer hub.Stop()

	collector := NewMetricsCollector(hub)

	// Create a receiving client
	client := &Client{
		hub:    hub,
		taskID: "task-1",
		send:   make(chan []byte, 256),
	}
	hub.Register(client)
	time.Sleep(10 * time.Millisecond)

	// Send metrics
	collector.SendMetrics("task-1", VMMetrics{
		CPUPercent:    45.5,
		MemoryBytes:   2147483648, // 2GB
		MemoryMaxBytes: 4294967296, // 4GB
		NetworkRxBytes: 1024000,
		NetworkTxBytes: 512000,
	})

	select {
	case msg := <-client.send:
		if len(msg) == 0 {
			t.Error("expected metrics message")
		}
	case <-time.After(100 * time.Millisecond):
		t.Error("timeout waiting for metrics message")
	}
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./pkg/dashboard/... -run TestMetricsCollector -v`
Expected: FAIL - MetricsCollector undefined

**Step 3: Write minimal implementation**

Create `pkg/dashboard/metrics.go`:

```go
package dashboard

import (
	"encoding/json"
	"time"
)

// VMMetrics represents resource metrics for a VM.
type VMMetrics struct {
	CPUPercent     float64 `json:"cpu_percent"`
	MemoryBytes    int64   `json:"memory_bytes"`
	MemoryMaxBytes int64   `json:"memory_max_bytes"`
	NetworkRxBytes int64   `json:"network_rx_bytes"`
	NetworkTxBytes int64   `json:"network_tx_bytes"`
	DiskUsedBytes  int64   `json:"disk_used_bytes"`
	DiskTotalBytes int64   `json:"disk_total_bytes"`
}

// MetricsMessage is the WebSocket message for metrics updates.
type MetricsMessage struct {
	Type      string    `json:"type"` // "metrics"
	TaskID    string    `json:"task_id"`
	Metrics   VMMetrics `json:"metrics"`
	Timestamp time.Time `json:"timestamp"`
}

// MetricsCollector manages metrics collection and broadcasting.
type MetricsCollector struct {
	hub *Hub
}

// NewMetricsCollector creates a new metrics collector.
func NewMetricsCollector(hub *Hub) *MetricsCollector {
	return &MetricsCollector{hub: hub}
}

// SendMetrics broadcasts metrics to subscribed clients.
func (m *MetricsCollector) SendMetrics(taskID string, metrics VMMetrics) {
	msg := MetricsMessage{
		Type:      "metrics",
		TaskID:    taskID,
		Metrics:   metrics,
		Timestamp: time.Now(),
	}

	data, err := json.Marshal(msg)
	if err != nil {
		return
	}

	m.hub.Broadcast(taskID, data)
}

// FormatBytes formats bytes as human-readable string.
func FormatBytes(bytes int64) string {
	const (
		KB = 1024
		MB = KB * 1024
		GB = MB * 1024
	)

	switch {
	case bytes >= GB:
		return formatFloat(float64(bytes)/GB) + " GB"
	case bytes >= MB:
		return formatFloat(float64(bytes)/MB) + " MB"
	case bytes >= KB:
		return formatFloat(float64(bytes)/KB) + " KB"
	default:
		return formatFloat(float64(bytes)) + " B"
	}
}

func formatFloat(f float64) string {
	if f == float64(int64(f)) {
		return string(rune(int64(f) + '0'))
	}
	// Simple formatting without fmt to avoid import
	i := int64(f * 10)
	return string(rune(i/10+'0')) + "." + string(rune(i%10+'0'))
}
```

**Step 4: Run test to verify it passes**

Run: `go test ./pkg/dashboard/... -run TestMetricsCollector -v`
Expected: PASS

**Step 5: Commit**

```bash
git add pkg/dashboard/metrics.go pkg/dashboard/metrics_test.go
git commit -m "feat(dashboard): add metrics streaming infrastructure"
```

---

## Task 7: Add Firecracker Metrics Collection

**Files:**
- Modify: `/home/jesse/git/stockyard/pkg/firecracker/api.go` (or create metrics.go)
- Create: `/home/jesse/git/stockyard/pkg/firecracker/metrics_test.go`

**Step 1: Write the failing test**

Create `pkg/firecracker/metrics_test.go`:

```go
package firecracker

import (
	"testing"
)

func TestParseFirecrackerMetrics(t *testing.T) {
	// Sample Firecracker metrics JSON
	metricsJSON := `{
		"utc_timestamp_ms": 1234567890,
		"vcpu": {
			"exit_io_in": 100,
			"exit_io_out": 50
		},
		"net": {
			"rx_bytes": 1024,
			"tx_bytes": 512
		}
	}`

	metrics, err := ParseMetrics([]byte(metricsJSON))
	if err != nil {
		t.Fatalf("failed to parse metrics: %v", err)
	}

	if metrics.Net.RxBytes != 1024 {
		t.Errorf("expected rx_bytes 1024, got %d", metrics.Net.RxBytes)
	}
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./pkg/firecracker/... -run TestParseFirecrackerMetrics -v`
Expected: FAIL - ParseMetrics undefined

**Step 3: Write minimal implementation**

Create `pkg/firecracker/metrics.go`:

```go
package firecracker

import (
	"encoding/json"
)

// FirecrackerMetrics represents the metrics returned by Firecracker.
type FirecrackerMetrics struct {
	UTCTimestampMs int64 `json:"utc_timestamp_ms"`
	VCPU           struct {
		ExitIOIn  int64 `json:"exit_io_in"`
		ExitIOOut int64 `json:"exit_io_out"`
	} `json:"vcpu"`
	Net struct {
		RxBytes  int64 `json:"rx_bytes"`
		TxBytes  int64 `json:"tx_bytes"`
		RxFrames int64 `json:"rx_frames"`
		TxFrames int64 `json:"tx_frames"`
	} `json:"net"`
	Block struct {
		ReadBytes  int64 `json:"read_bytes"`
		WriteBytes int64 `json:"write_bytes"`
	} `json:"block"`
}

// ParseMetrics parses Firecracker metrics JSON.
func ParseMetrics(data []byte) (*FirecrackerMetrics, error) {
	var metrics FirecrackerMetrics
	if err := json.Unmarshal(data, &metrics); err != nil {
		return nil, err
	}
	return &metrics, nil
}
```

**Step 4: Run test to verify it passes**

Run: `go test ./pkg/firecracker/... -run TestParseFirecrackerMetrics -v`
Expected: PASS

**Step 5: Commit**

```bash
git add pkg/firecracker/metrics.go pkg/firecracker/metrics_test.go
git commit -m "feat(firecracker): add metrics parsing"
```

---

## Task 7A: Add GetMetrics to Firecracker APIClient

**Files:**
- Modify: `/home/jesse/git/stockyard/pkg/firecracker/api.go`
- Modify: `/home/jesse/git/stockyard/pkg/firecracker/api_test.go`

**Context:** The Firecracker API exposes metrics at GET /metrics. We need to call this to get real metrics instead of placeholders.

**Step 1: Write the failing test**

Add to `pkg/firecracker/api_test.go`:

```go
func TestAPIClient_GetMetrics(t *testing.T) {
	// Create mock server
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/metrics" && r.Method == "GET" {
			json.NewEncoder(w).Encode(map[string]interface{}{
				"utc_timestamp_ms": 1234567890,
				"vcpu": map[string]int64{
					"exit_io_in":  100,
					"exit_io_out": 50,
				},
				"net": map[string]int64{
					"rx_bytes": 1024,
					"tx_bytes": 512,
				},
			})
			return
		}
		http.NotFound(w, r)
	}))

	// Use Unix socket
	tmpDir := t.TempDir()
	socketPath := filepath.Join(tmpDir, "api.sock")
	l, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatalf("failed to listen: %v", err)
	}
	server.Listener = l
	server.Start()
	defer server.Close()

	client := NewAPIClient(socketPath)
	metrics, err := client.GetMetrics(context.Background())
	if err != nil {
		t.Fatalf("GetMetrics failed: %v", err)
	}

	if metrics.Net.RxBytes != 1024 {
		t.Errorf("expected rx_bytes 1024, got %d", metrics.Net.RxBytes)
	}
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./pkg/firecracker/... -run TestAPIClient_GetMetrics -v`
Expected: FAIL - GetMetrics undefined

**Step 3: Write minimal implementation**

Add to `pkg/firecracker/api.go`:

```go
// GetMetrics fetches metrics from the Firecracker API.
func (c *APIClient) GetMetrics(ctx context.Context) (*FirecrackerMetrics, error) {
	resp, err := c.doRequest(ctx, "GET", "/metrics", nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("get metrics failed: %s - %s", resp.Status, string(body))
	}

	var metrics FirecrackerMetrics
	if err := json.NewDecoder(resp.Body).Decode(&metrics); err != nil {
		return nil, fmt.Errorf("decode metrics: %w", err)
	}

	return &metrics, nil
}
```

**Step 4: Run test to verify it passes**

Run: `go test ./pkg/firecracker/... -run TestAPIClient_GetMetrics -v`
Expected: PASS

**Step 5: Commit**

```bash
git add pkg/firecracker/api.go pkg/firecracker/api_test.go
git commit -m "feat(firecracker): add GetMetrics to APIClient"
```

---

## Task 8: Add Metrics Polling to Daemon

**Files:**
- Modify: `/home/jesse/git/stockyard/pkg/daemon/daemon.go`
- Create: `/home/jesse/git/stockyard/pkg/daemon/metrics.go`

**Step 1: Write the failing test**

Create `pkg/daemon/metrics_test.go`:

```go
package daemon

import (
	"testing"
	"time"
)

func TestMetricsPoller_StartsAndStops(t *testing.T) {
	poller := NewMetricsPoller(nil, nil, 100*time.Millisecond)

	poller.Start()
	time.Sleep(50 * time.Millisecond)

	if !poller.Running() {
		t.Error("expected poller to be running")
	}

	poller.Stop()
	time.Sleep(50 * time.Millisecond)

	if poller.Running() {
		t.Error("expected poller to be stopped")
	}
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./pkg/daemon/... -run TestMetricsPoller -v`
Expected: FAIL - MetricsPoller undefined

**Step 3: Write minimal implementation**

Create `pkg/daemon/metrics.go`:

```go
package daemon

import (
	"context"
	"sync"
	"time"
)

// MetricsSink receives metrics updates.
type MetricsSink interface {
	SendMetrics(taskID string, metrics interface{})
}

// MetricsPoller periodically collects metrics from running VMs.
type MetricsPoller struct {
	daemon   *Daemon
	sink     MetricsSink
	interval time.Duration
	running  bool
	stop     chan struct{}
	mu       sync.RWMutex
}

// NewMetricsPoller creates a new metrics poller.
func NewMetricsPoller(daemon *Daemon, sink MetricsSink, interval time.Duration) *MetricsPoller {
	return &MetricsPoller{
		daemon:   daemon,
		sink:     sink,
		interval: interval,
		stop:     make(chan struct{}),
	}
}

// Start begins polling for metrics.
func (p *MetricsPoller) Start() {
	p.mu.Lock()
	if p.running {
		p.mu.Unlock()
		return
	}
	p.running = true
	p.stop = make(chan struct{})
	p.mu.Unlock()

	go p.run()
}

// Stop stops the metrics poller.
func (p *MetricsPoller) Stop() {
	p.mu.Lock()
	defer p.mu.Unlock()

	if !p.running {
		return
	}

	close(p.stop)
	p.running = false
}

// Running returns whether the poller is active.
func (p *MetricsPoller) Running() bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.running
}

func (p *MetricsPoller) run() {
	ticker := time.NewTicker(p.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			p.collectMetrics()
		case <-p.stop:
			return
		}
	}
}

func (p *MetricsPoller) collectMetrics() {
	if p.daemon == nil || p.sink == nil {
		return
	}

	ctx := context.Background()
	tasks, err := p.daemon.state.ListTasks("")
	if err != nil {
		return
	}

	for _, task := range tasks {
		if task.Status != "running" || task.VMID == "" {
			continue
		}

		// Get VM directory and API socket path
		vmDir := filepath.Join(p.daemon.cfg.ZFS.VMsPath, task.VMID)
		apiSocketPath := filepath.Join(vmDir, "api.sock")

		// Check if socket exists
		if _, err := os.Stat(apiSocketPath); os.IsNotExist(err) {
			continue
		}

		// Fetch metrics from Firecracker API
		apiClient := firecracker.NewAPIClient(apiSocketPath)
		fcMetrics, err := apiClient.GetMetrics(ctx)
		if err != nil {
			continue // Skip this VM if we can't get metrics
		}

		// Convert to dashboard metrics format
		metrics := dashboard.VMMetrics{
			CPUPercent:     0, // CPU % requires calculation from vcpu counters over time
			MemoryBytes:    0, // Memory requires reading from machine-config
			MemoryMaxBytes: 0,
			NetworkRxBytes: fcMetrics.Net.RxBytes,
			NetworkTxBytes: fcMetrics.Net.TxBytes,
			DiskUsedBytes:  fcMetrics.Block.ReadBytes + fcMetrics.Block.WriteBytes,
			DiskTotalBytes: 0, // Would need to query disk size
		}

		p.sink.SendMetrics(task.ID, metrics)
	}
}
```

**Step 4: Run test to verify it passes**

Run: `go test ./pkg/daemon/... -run TestMetricsPoller -v`
Expected: PASS

**Step 5: Commit**

```bash
git add pkg/daemon/metrics.go pkg/daemon/metrics_test.go
git commit -m "feat(daemon): add metrics polling infrastructure"
```

---

## Task 9: Update VM Detail Template for Live Updates

**Files:**
- Modify: `/home/jesse/git/stockyard/pkg/dashboard/templates/vm_detail.html`

**Step 1: Add WebSocket connection and live metrics display**

Update the Resources section in `vm_detail.html`:

```html
<!-- Resources panel -->
<div class="bg-white rounded-lg border border-gray-200 p-4"
     x-data="{
         cpu: '--',
         memory: '--',
         memoryMax: '--',
         network: '--',
         disk: '--',
         connected: false
     }"
     x-init="
         const ws = new WebSocket('ws://' + window.location.host + '/ws?task={{.Task.ID}}');
         ws.onopen = () => { connected = true; };
         ws.onclose = () => { connected = false; };
         ws.onmessage = (e) => {
             const data = JSON.parse(e.data);
             if (data.type === 'metrics') {
                 cpu = data.metrics.cpu_percent.toFixed(1) + '%';
                 memory = formatBytes(data.metrics.memory_bytes);
                 memoryMax = formatBytes(data.metrics.memory_max_bytes);
                 network = formatBytes(data.metrics.network_rx_bytes + data.metrics.network_tx_bytes) + '/s';
                 disk = formatBytes(data.metrics.disk_used_bytes);
             }
         };
     ">
    <div class="flex items-center justify-between mb-4">
        <h2 class="font-semibold text-gray-900">Resources</h2>
        <span x-show="connected" class="text-xs text-green-600 flex items-center gap-1">
            <span class="w-2 h-2 bg-green-500 rounded-full animate-pulse"></span>
            Live
        </span>
    </div>
    <div class="grid grid-cols-4 gap-4">
        <div class="text-center p-4 bg-gray-50 rounded-lg">
            <div class="text-2xl font-bold text-gray-900" x-text="cpu">--</div>
            <div class="text-sm text-gray-500">CPU</div>
        </div>
        <div class="text-center p-4 bg-gray-50 rounded-lg">
            <div class="text-2xl font-bold text-gray-900" x-text="memory">--</div>
            <div class="text-sm text-gray-500">Memory</div>
        </div>
        <div class="text-center p-4 bg-gray-50 rounded-lg">
            <div class="text-2xl font-bold text-gray-900" x-text="network">--</div>
            <div class="text-sm text-gray-500">Network</div>
        </div>
        <div class="text-center p-4 bg-gray-50 rounded-lg">
            <div class="text-2xl font-bold text-gray-900" x-text="disk">--</div>
            <div class="text-sm text-gray-500">Disk</div>
        </div>
    </div>
</div>
```

Add JavaScript helper function at the bottom of the template:

```html
<script>
function formatBytes(bytes) {
    if (bytes === 0) return '0 B';
    const k = 1024;
    const sizes = ['B', 'KB', 'MB', 'GB'];
    const i = Math.floor(Math.log(bytes) / Math.log(k));
    return parseFloat((bytes / Math.pow(k, i)).toFixed(1)) + ' ' + sizes[i];
}

function copySSH(host) {
    // ... existing function
}
</script>
```

**Step 2: Update Logs section for live streaming**

Replace the Logs panel:

```html
<!-- Logs panel -->
<div class="bg-white rounded-lg border border-gray-200 p-4"
     x-data="{
         logs: [],
         mode: 'live',
         filter: '',
         paused: false,
         connected: false,
         maxLogs: 500
     }"
     x-init="
         const ws = new WebSocket('ws://' + window.location.host + '/ws?task={{.Task.ID}}');
         ws.onopen = () => { connected = true; };
         ws.onclose = () => { connected = false; };
         ws.onmessage = (e) => {
             if (paused) return;
             const data = JSON.parse(e.data);
             if (data.type === 'log') {
                 logs.push({
                     time: new Date(data.timestamp).toLocaleTimeString(),
                     stream: data.stream,
                     line: data.line
                 });
                 if (logs.length > maxLogs) {
                     logs = logs.slice(-maxLogs);
                 }
                 $nextTick(() => {
                     const el = $refs.logContainer;
                     if (el) el.scrollTop = el.scrollHeight;
                 });
             }
         };
     ">
    <div class="flex items-center justify-between mb-4">
        <div class="flex items-center gap-2">
            <h2 class="font-semibold text-gray-900">Logs</h2>
            <button @click="mode = 'live'"
                    :class="mode === 'live' ? 'bg-green-100 text-green-700' : 'bg-gray-100'"
                    class="px-2 py-1 text-xs rounded">
                <span x-show="connected && !paused" class="inline-block w-2 h-2 bg-green-500 rounded-full mr-1 animate-pulse"></span>
                Live
            </button>
            <button @click="mode = 'history'"
                    :class="mode === 'history' ? 'bg-blue-100 text-blue-700' : 'bg-gray-100'"
                    class="px-2 py-1 text-xs rounded">
                History
            </button>
        </div>
        <div class="flex items-center gap-2">
            <input type="text" x-model="filter" placeholder="Filter..."
                   class="px-2 py-1 text-sm border border-gray-300 rounded">
            <button @click="paused = !paused"
                    :class="paused ? 'bg-yellow-100 text-yellow-700' : 'bg-gray-100'"
                    class="px-2 py-1 text-xs rounded">
                <span x-text="paused ? 'Resume' : 'Pause'"></span>
            </button>
            <button @click="logs = []" class="px-2 py-1 text-xs bg-gray-100 rounded">
                Clear
            </button>
        </div>
    </div>
    <div x-ref="logContainer"
         class="bg-gray-900 text-gray-300 rounded-lg p-4 font-mono text-sm h-64 overflow-auto">
        <template x-if="logs.length === 0">
            <p class="text-gray-500">Waiting for logs...</p>
        </template>
        <template x-for="(log, i) in logs.filter(l => !filter || l.line.includes(filter))" :key="i">
            <div class="flex gap-2 hover:bg-gray-800">
                <span class="text-gray-500 shrink-0" x-text="log.time"></span>
                <span :class="log.stream === 'stderr' ? 'text-red-400' : 'text-gray-300'" x-text="log.line"></span>
            </div>
        </template>
    </div>
</div>
```

**Step 3: Run template test**

Run: `go test ./pkg/dashboard/... -run TestTemplates_VMDetail -v`
Expected: PASS

**Step 4: Commit**

```bash
git add pkg/dashboard/templates/vm_detail.html
git commit -m "feat(dashboard): add live metrics and log streaming to VM detail"
```

---

## Task 10: Add Status Update Broadcasting

**Files:**
- Modify: `/home/jesse/git/stockyard/pkg/dashboard/server.go`
- Create: `/home/jesse/git/stockyard/pkg/dashboard/status.go`

**Step 1: Write the failing test**

Create `pkg/dashboard/status_test.go`:

```go
package dashboard

import (
	"testing"
	"time"
)

func TestStatusBroadcaster_SendsUpdates(t *testing.T) {
	hub := NewHub()
	go hub.Run()
	defer hub.Stop()

	broadcaster := NewStatusBroadcaster(hub)

	client := &Client{
		hub:    hub,
		taskID: "",  // Subscribe to all
		send:   make(chan []byte, 256),
	}
	hub.Register(client)
	time.Sleep(10 * time.Millisecond)

	broadcaster.TaskStatusChanged("task-1", "running", "stopped")

	select {
	case msg := <-client.send:
		if len(msg) == 0 {
			t.Error("expected status message")
		}
	case <-time.After(100 * time.Millisecond):
		t.Error("timeout waiting for status message")
	}
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./pkg/dashboard/... -run TestStatusBroadcaster -v`
Expected: FAIL - StatusBroadcaster undefined

**Step 3: Write minimal implementation**

Create `pkg/dashboard/status.go`:

```go
package dashboard

import (
	"encoding/json"
	"time"
)

// StatusMessage represents a VM status change.
type StatusMessage struct {
	Type       string    `json:"type"` // "status"
	TaskID     string    `json:"task_id"`
	OldStatus  string    `json:"old_status"`
	NewStatus  string    `json:"new_status"`
	Timestamp  time.Time `json:"timestamp"`
}

// StatusBroadcaster sends status updates to clients.
type StatusBroadcaster struct {
	hub *Hub
}

// NewStatusBroadcaster creates a new status broadcaster.
func NewStatusBroadcaster(hub *Hub) *StatusBroadcaster {
	return &StatusBroadcaster{hub: hub}
}

// TaskStatusChanged broadcasts a status change.
func (s *StatusBroadcaster) TaskStatusChanged(taskID, oldStatus, newStatus string) {
	msg := StatusMessage{
		Type:      "status",
		TaskID:    taskID,
		OldStatus: oldStatus,
		NewStatus: newStatus,
		Timestamp: time.Now(),
	}

	data, err := json.Marshal(msg)
	if err != nil {
		return
	}

	// Broadcast to all clients (status changes are fleet-wide events)
	s.hub.BroadcastAll(data)
}

// TaskCreated broadcasts that a new task was created.
func (s *StatusBroadcaster) TaskCreated(taskID string) {
	s.TaskStatusChanged(taskID, "", "pending")
}

// TaskDestroyed broadcasts that a task was destroyed.
func (s *StatusBroadcaster) TaskDestroyed(taskID string) {
	s.TaskStatusChanged(taskID, "stopped", "destroyed")
}
```

**Step 4: Run test to verify it passes**

Run: `go test ./pkg/dashboard/... -run TestStatusBroadcaster -v`
Expected: PASS

**Step 5: Commit**

```bash
git add pkg/dashboard/status.go pkg/dashboard/status_test.go
git commit -m "feat(dashboard): add status change broadcasting"
```

---

## Task 10A: Wire Status Changes to Broadcaster

**Files:**
- Modify: `/home/jesse/git/stockyard/pkg/daemon/state.go`
- Modify: `/home/jesse/git/stockyard/pkg/daemon/state_test.go`

**Context:** The StatusBroadcaster exists but nothing calls `TaskStatusChanged()`. We need to add a callback mechanism to State so it notifies when task status changes.

**Step 1: Write the failing test**

Add to `pkg/daemon/state_test.go`:

```go
func TestState_StatusChangeCallback(t *testing.T) {
	state, err := NewState()
	if err != nil {
		t.Fatalf("NewState: %v", err)
	}
	defer state.Close()

	// Track callback invocations
	var called bool
	var receivedTaskID, receivedOld, receivedNew string

	state.SetStatusChangeCallback(func(taskID, oldStatus, newStatus string) {
		called = true
		receivedTaskID = taskID
		receivedOld = oldStatus
		receivedNew = newStatus
	})

	// Create a task
	task := &Task{ID: "test-callback", Status: "pending"}
	if err := state.CreateTask(task); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}

	// Update status
	if err := state.UpdateTaskStatus("test-callback", "running"); err != nil {
		t.Fatalf("UpdateTaskStatus: %v", err)
	}

	if !called {
		t.Error("callback was not called")
	}
	if receivedTaskID != "test-callback" {
		t.Errorf("expected taskID test-callback, got %s", receivedTaskID)
	}
	if receivedOld != "pending" {
		t.Errorf("expected old status pending, got %s", receivedOld)
	}
	if receivedNew != "running" {
		t.Errorf("expected new status running, got %s", receivedNew)
	}
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./pkg/daemon/... -run TestState_StatusChangeCallback -v`
Expected: FAIL - SetStatusChangeCallback undefined

**Step 3: Write minimal implementation**

Add to `pkg/daemon/state.go`:

```go
// StatusChangeCallback is called when a task's status changes.
type StatusChangeCallback func(taskID, oldStatus, newStatus string)

// Add to State struct:
type State struct {
	// ... existing fields
	statusCallback StatusChangeCallback
	callbackMu     sync.RWMutex
}

// SetStatusChangeCallback sets the callback for status changes.
func (s *State) SetStatusChangeCallback(cb StatusChangeCallback) {
	s.callbackMu.Lock()
	defer s.callbackMu.Unlock()
	s.statusCallback = cb
}

// Update UpdateTaskStatus to call the callback:
func (s *State) UpdateTaskStatus(id, status string) error {
	// Get old status first
	task, err := s.GetTask(id)
	if err != nil {
		return err
	}
	oldStatus := task.Status

	// Update in database
	_, err = s.db.Exec("UPDATE tasks SET status = ?, updated_at = ? WHERE id = ?",
		status, time.Now(), id)
	if err != nil {
		return err
	}

	// Notify callback if registered
	s.callbackMu.RLock()
	cb := s.statusCallback
	s.callbackMu.RUnlock()

	if cb != nil && oldStatus != status {
		cb(id, oldStatus, status)
	}

	return nil
}
```

**Step 4: Run test to verify it passes**

Run: `go test ./pkg/daemon/... -run TestState_StatusChangeCallback -v`
Expected: PASS

**Step 5: Commit**

```bash
git add pkg/daemon/state.go pkg/daemon/state_test.go
git commit -m "feat(daemon): add status change callback to State"
```

---

## Task 11: Update Fleet Template for Live Status

**Files:**
- Modify: `/home/jesse/git/stockyard/pkg/dashboard/templates/fleet.html`

**Step 1: Add WebSocket status updates to fleet page**

Update the fleet.html template to include WebSocket connection for live updates:

Add after the opening `<body>` tag:

```html
<body class="bg-gray-50 text-gray-900 h-screen flex flex-col"
      x-data="{
          statusUpdates: {},
          connected: false
      }"
      x-init="
          const ws = new WebSocket('ws://' + window.location.host + '/ws');
          ws.onopen = () => { connected = true; };
          ws.onclose = () => { connected = false; };
          ws.onmessage = (e) => {
              const data = JSON.parse(e.data);
              if (data.type === 'status') {
                  statusUpdates[data.task_id] = data.new_status;
                  // Refresh page on status changes (simple approach for now)
                  if (data.new_status === 'destroyed' || data.old_status === '') {
                      setTimeout(() => location.reload(), 500);
                  }
              }
          };
      ">
```

Add a connection indicator next to the title:

```html
<div class="mb-6 flex items-center justify-between">
    <h1 class="text-2xl font-bold text-gray-900">Fleet Overview</h1>
    <span x-show="connected" class="text-xs text-green-600 flex items-center gap-1">
        <span class="w-2 h-2 bg-green-500 rounded-full animate-pulse"></span>
        Live
    </span>
</div>
```

**Step 2: Update status display to use live data**

Update the status cell in the VM table to check for live updates:

```html
<td class="px-4 py-3">
    {{if eq .Status "running"}}
    <span class="inline-flex items-center gap-1 text-green-700"
          :class="statusUpdates['{{.ID}}'] === 'stopped' ? 'text-gray-500' : 'text-green-700'">
        <span class="w-2 h-2 rounded-full"
              :class="statusUpdates['{{.ID}}'] === 'stopped' ? 'bg-gray-400' : 'bg-green-500'"></span>
        <span x-text="statusUpdates['{{.ID}}'] || 'running'">running</span>
    </span>
    {{else if eq .Status "stopped"}}
    <span class="inline-flex items-center gap-1 text-gray-500">
        <span class="w-2 h-2 bg-gray-400 rounded-full"></span>
        stopped
    </span>
    {{else if eq .Status "failed"}}
    <span class="inline-flex items-center gap-1 text-red-700">
        <span class="w-2 h-2 bg-red-500 rounded-full"></span>
        failed
    </span>
    {{else}}
    <span class="text-gray-500">{{.Status}}</span>
    {{end}}
</td>
```

**Step 3: Test templates still load**

Run: `go test ./pkg/dashboard/... -run TestTemplates -v`
Expected: PASS

**Step 4: Commit**

```bash
git add pkg/dashboard/templates/fleet.html
git commit -m "feat(dashboard): add live status updates to fleet page"
```

---

## Task 12: Wire Real-time Components into Daemon

**Files:**
- Modify: `/home/jesse/git/stockyard/pkg/daemon/daemon.go`

**Step 1: Update daemon to initialize real-time components**

Add imports:

```go
import (
	// ... existing imports
	"github.com/obra/stockyard/pkg/dashboard"
	"github.com/obra/stockyard/pkg/firecracker"
)
```

Add fields to Daemon struct:

```go
type Daemon struct {
	// ... existing fields
	httpServer        *http.Server
	dashboardServer   *dashboard.Server
	metricsPoller     *MetricsPoller
	logTailer         *LogTailer
	statusBroadcaster *dashboard.StatusBroadcaster
}
```

Update Start() to wire up real-time components:

```go
// Start HTTP server if enabled
if d.cfg.HTTP.Enabled {
	// Create dashboard facade and adapter
	facade := NewDashboardFacade(d.state, d.tasks)
	adapter := dashboard.NewDaemonAdapter(facade)
	d.dashboardServer = dashboard.NewServer(adapter)
	tsClient := tailscale.NewLocalClient()
	handler := dashboard.AuthMiddleware(d.dashboardServer, tsClient)

	d.httpServer = &http.Server{
		Addr:    d.cfg.HTTP.Addr,
		Handler: handler,
	}

	// Create real-time components
	hub := d.dashboardServer.Hub()

	// Status broadcaster - wire to state callback
	d.statusBroadcaster = dashboard.NewStatusBroadcaster(hub)
	d.state.SetStatusChangeCallback(func(taskID, oldStatus, newStatus string) {
		d.statusBroadcaster.TaskStatusChanged(taskID, oldStatus, newStatus)
	})

	// Log streamer and tailer
	logStreamer := dashboard.NewLogStreamer(hub)
	d.logTailer = NewLogTailer(&dashboardLogSink{logStreamer})

	// Metrics collector and poller
	metricsCollector := dashboard.NewMetricsCollector(hub)
	d.metricsPoller = NewMetricsPoller(d, &dashboardMetricsSink{metricsCollector}, 5*time.Second)
	d.metricsPoller.Start()

	go func() {
		fmt.Printf("Starting HTTP server on %s\n", d.cfg.HTTP.Addr)
		if err := d.httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			fmt.Printf("HTTP server error: %v\n", err)
		}
	}()
}
```

Add adapters for log and metrics sinks:

```go
type dashboardLogSink struct {
	streamer *dashboard.LogStreamer
}

func (s *dashboardLogSink) SendLog(taskID, stream, line string) {
	s.streamer.SendLog(taskID, stream, line)
}

type dashboardMetricsSink struct {
	collector *dashboard.MetricsCollector
}

func (s *dashboardMetricsSink) SendMetrics(taskID string, metrics interface{}) {
	if m, ok := metrics.(dashboard.VMMetrics); ok {
		s.collector.SendMetrics(taskID, m)
	}
}
```

Update Stop():

```go
func (d *Daemon) Stop() {
	// Stop log tailer
	if d.logTailer != nil {
		d.logTailer.Stop()
	}

	// Stop metrics polling
	if d.metricsPoller != nil {
		d.metricsPoller.Stop()
	}

	// Shutdown HTTP server
	if d.httpServer != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		d.httpServer.Shutdown(ctx)
	}

	// Close dashboard server
	if d.dashboardServer != nil {
		d.dashboardServer.Close()
	}

	// ... existing gRPC shutdown
}
```

**Step 2: Start log tailing when task starts**

Add to TaskManager.StartTask() after VM is created:

```go
// Start log tailing if dashboard is enabled
if tm.daemon.logTailer != nil && vm != nil {
	vmDir := filepath.Join(tm.daemon.cfg.ZFS.VMsPath, vm.ID)
	tm.daemon.logTailer.TailFile(taskID, "stdout", filepath.Join(vmDir, "stdout.log"))
	tm.daemon.logTailer.TailFile(taskID, "stderr", filepath.Join(vmDir, "stderr.log"))
}
```

Add to TaskManager.StopTask() before stopping VM:

```go
// Stop log tailing
if tm.daemon.logTailer != nil {
	tm.daemon.logTailer.StopTask(taskID)
}
```

**Step 2: Run tests**

Run: `go test ./pkg/daemon/... -v`
Expected: PASS (may have some compilation adjustments needed)

**Step 3: Commit**

```bash
git add pkg/daemon/daemon.go
git commit -m "feat(daemon): wire real-time components for dashboard"
```

---

## Summary

Phase 2 implementation creates:
- WebSocket hub for managing client connections (Task 2)
- WebSocket handler with gorilla/websocket (Task 3)
- Log streaming infrastructure (Task 5)
- **Log tailer that reads VM stdout/stderr log files** (Task 5A - NEW)
- Metrics streaming infrastructure (Task 6)
- Firecracker metrics parsing (Task 7)
- **GetMetrics() API call to Firecracker** (Task 7A - NEW)
- Metrics polling in daemon with real Firecracker data (Task 8 - FIXED)
- Live-updating VM detail template (Task 9)
- Status change broadcasting (Task 10)
- **Status change callback in State for broadcasting** (Task 10A - NEW)
- Live status updates on fleet page (Task 11)
- Full wiring of all real-time components (Task 12 - EXPANDED)

**Gaps fixed in this revision:**
1. Task 5A: LogTailer reads `stdout.log`/`stderr.log` from VM directories
2. Task 7A: GetMetrics() calls Firecracker `/metrics` API endpoint
3. Task 8: collectMetrics() now uses real Firecracker metrics instead of placeholders
4. Task 10A: State.SetStatusChangeCallback() notifies when task status changes
5. Task 12: Full wiring including log tailing start/stop with task lifecycle

After Phase 2, the dashboard:
- Shows live CPU, memory, network, disk metrics (from Firecracker API)
- Streams logs in real-time with pause/resume/filter (from VM log files)
- Updates VM status live without page refresh (via State callback)
- Connects via WebSocket for efficient real-time updates

Phase 3 adds polish: grouped cards, split view, sparklines, charts, activity feed, alerts, and responsive layouts.
