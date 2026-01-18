# Dashboard Phase 3: Polish Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Add visual polish and UX improvements: split view layout, sparkline charts, activity feed, alert indicators, responsive design, log controls, and filtering.

**Architecture:** Extends Phase 2 with Chart.js for sparklines, additional WebSocket message types for activity feed, CSS media queries and Alpine.js for responsive behavior, log history storage.

**Tech Stack:** Chart.js (CDN), Tailwind CSS responsive utilities, Alpine.js for interactive components

**Prerequisites:** Phase 1 and Phase 2 must be complete.

---

## Task Overview

| Task | Description |
|------|-------------|
| 1 | Add Chart.js for Sparklines |
| 2 | Update Resources Panel with Sparklines |
| 2A | Add Log Pause/Resume Controls |
| 2B | Add Historical Log Search |
| 2C | Add Log Filter Input |
| 3 | Implement Split View Layout |
| 3A | Add "By Repo" and "By Owner" Tabs |
| 3B | Add Double-Click to Open Detail |
| 3C | Add Sparklines to Preview Panel |
| 4 | Add Activity Feed Infrastructure |
| 5 | Add Activity Feed UI |
| 5A | Add User Avatar to Header |
| 6 | Add Alert Infrastructure |
| 6A | Add Unresponsive VM Detection |
| 7 | Add Alert Badge to Fleet Page |
| 8 | Add Responsive Layout |
| 9 | Final Integration and Polish |

---

## Task 1: Add Chart.js for Sparklines

**Files:**
- Modify: `/home/jesse/git/stockyard/pkg/dashboard/templates/vm_detail.html`

**Step 1: Add Chart.js CDN**

Add to the `<head>` section:

```html
<script src="https://cdn.jsdelivr.net/npm/chart.js@4.4.1/dist/chart.umd.min.js"></script>
```

**Step 2: Create sparkline component**

Add a reusable sparkline component using Alpine.js. Add before the closing `</body>` tag:

```html
<script>
document.addEventListener('alpine:init', () => {
    Alpine.data('sparkline', (canvasId, color = '#10b981') => ({
        chart: null,
        data: [],
        maxPoints: 30,

        init() {
            const ctx = document.getElementById(canvasId);
            if (!ctx) return;

            this.chart = new Chart(ctx, {
                type: 'line',
                data: {
                    labels: [],
                    datasets: [{
                        data: [],
                        borderColor: color,
                        backgroundColor: color + '20',
                        fill: true,
                        tension: 0.4,
                        pointRadius: 0,
                        borderWidth: 2,
                    }]
                },
                options: {
                    responsive: true,
                    maintainAspectRatio: false,
                    plugins: {
                        legend: { display: false },
                        tooltip: { enabled: false }
                    },
                    scales: {
                        x: { display: false },
                        y: {
                            display: false,
                            min: 0,
                            max: 100
                        }
                    },
                    animation: { duration: 0 }
                }
            });
        },

        addPoint(value) {
            this.data.push(value);
            if (this.data.length > this.maxPoints) {
                this.data.shift();
            }

            if (this.chart) {
                this.chart.data.labels = this.data.map((_, i) => i);
                this.chart.data.datasets[0].data = this.data;
                this.chart.update('none');
            }
        }
    }));
});
</script>
```

**Step 3: Commit**

```bash
git add pkg/dashboard/templates/vm_detail.html
git commit -m "feat(dashboard): add Chart.js sparkline component"
```

---

## Task 2: Update Resources Panel with Sparklines

**Files:**
- Modify: `/home/jesse/git/stockyard/pkg/dashboard/templates/vm_detail.html`

**Step 1: Replace static metrics with sparkline charts**

Update the Resources panel:

```html
<!-- Resources panel -->
<div class="bg-white rounded-lg border border-gray-200 p-4"
     x-data="{
         cpu: 0,
         memory: 0,
         memoryMax: 0,
         network: 0,
         disk: 0,
         connected: false,
         expanded: false,
         cpuHistory: [],
         memHistory: [],
         netHistory: []
     }"
     x-init="
         const ws = new WebSocket('ws://' + window.location.host + '/ws?task={{.Task.ID}}');
         ws.onopen = () => { connected = true; };
         ws.onclose = () => { connected = false; };
         ws.onmessage = (e) => {
             const data = JSON.parse(e.data);
             if (data.type === 'metrics') {
                 cpu = data.metrics.cpu_percent;
                 memory = data.metrics.memory_bytes;
                 memoryMax = data.metrics.memory_max_bytes;
                 network = data.metrics.network_rx_bytes + data.metrics.network_tx_bytes;
                 disk = data.metrics.disk_used_bytes;

                 // Update sparkline histories
                 cpuHistory.push(cpu);
                 if (cpuHistory.length > 30) cpuHistory.shift();
                 memHistory.push((memory / memoryMax) * 100);
                 if (memHistory.length > 30) memHistory.shift();
             }
         };
     ">
    <div class="flex items-center justify-between mb-4">
        <h2 class="font-semibold text-gray-900">Resources</h2>
        <div class="flex items-center gap-2">
            <span x-show="connected" class="text-xs text-green-600 flex items-center gap-1">
                <span class="w-2 h-2 bg-green-500 rounded-full animate-pulse"></span>
                Live
            </span>
            <button @click="expanded = !expanded"
                    class="text-xs text-blue-600 hover:underline"
                    x-text="expanded ? 'Collapse' : 'Expand'">
            </button>
        </div>
    </div>

    <!-- Compact view -->
    <div x-show="!expanded" class="grid grid-cols-4 gap-4">
        <div class="text-center p-3 bg-gray-50 rounded-lg">
            <div class="text-xl font-bold" :class="cpu > 80 ? 'text-red-600' : cpu > 50 ? 'text-yellow-600' : 'text-gray-900'"
                 x-text="cpu.toFixed(1) + '%'">--</div>
            <div class="text-xs text-gray-500">CPU</div>
            <div class="h-8 mt-2">
                <canvas id="cpuSparkline" class="w-full h-full"></canvas>
            </div>
        </div>
        <div class="text-center p-3 bg-gray-50 rounded-lg">
            <div class="text-xl font-bold text-gray-900" x-text="formatBytes(memory)">--</div>
            <div class="text-xs text-gray-500">Memory</div>
            <div class="h-8 mt-2">
                <canvas id="memSparkline" class="w-full h-full"></canvas>
            </div>
        </div>
        <div class="text-center p-3 bg-gray-50 rounded-lg">
            <div class="text-xl font-bold text-gray-900" x-text="formatBytes(network) + '/s'">--</div>
            <div class="text-xs text-gray-500">Network</div>
        </div>
        <div class="text-center p-3 bg-gray-50 rounded-lg">
            <div class="text-xl font-bold text-gray-900" x-text="formatBytes(disk)">--</div>
            <div class="text-xs text-gray-500">Disk</div>
        </div>
    </div>

    <!-- Expanded view with full charts -->
    <div x-show="expanded" class="space-y-4">
        <div class="grid grid-cols-2 gap-4">
            <div class="p-4 bg-gray-50 rounded-lg">
                <div class="flex justify-between mb-2">
                    <span class="text-sm font-medium">CPU Usage</span>
                    <span class="text-sm" :class="cpu > 80 ? 'text-red-600' : 'text-gray-600'"
                          x-text="cpu.toFixed(1) + '%'"></span>
                </div>
                <div class="h-32">
                    <canvas id="cpuChartFull"></canvas>
                </div>
            </div>
            <div class="p-4 bg-gray-50 rounded-lg">
                <div class="flex justify-between mb-2">
                    <span class="text-sm font-medium">Memory Usage</span>
                    <span class="text-sm text-gray-600"
                          x-text="formatBytes(memory) + ' / ' + formatBytes(memoryMax)"></span>
                </div>
                <div class="h-32">
                    <canvas id="memChartFull"></canvas>
                </div>
            </div>
        </div>
    </div>
</div>
```

**Step 2: Initialize sparkline charts**

Add initialization in the x-init or use separate script:

```html
<script>
// Initialize sparklines after Alpine loads
document.addEventListener('alpine:initialized', () => {
    const createSparkline = (id, color) => {
        const ctx = document.getElementById(id);
        if (!ctx) return null;
        return new Chart(ctx, {
            type: 'line',
            data: { labels: [], datasets: [{ data: [], borderColor: color, backgroundColor: color + '20', fill: true, tension: 0.4, pointRadius: 0, borderWidth: 1.5 }] },
            options: { responsive: true, maintainAspectRatio: false, plugins: { legend: { display: false }, tooltip: { enabled: false } }, scales: { x: { display: false }, y: { display: false, min: 0, max: 100 } }, animation: false }
        });
    };

    window.cpuSparkline = createSparkline('cpuSparkline', '#10b981');
    window.memSparkline = createSparkline('memSparkline', '#3b82f6');
});
</script>
```

**Step 3: Commit**

```bash
git add pkg/dashboard/templates/vm_detail.html
git commit -m "feat(dashboard): add sparkline charts to resources panel"
```

---

## Task 2A: Add Log Pause/Resume Controls

**Files:**
- Modify: `/home/jesse/git/stockyard/pkg/dashboard/templates/vm_detail.html`

**Step 1: Add pause/resume state and button**

Update the logs panel to include pause control:

```html
<!-- Logs panel -->
<div class="bg-white rounded-lg border border-gray-200 p-4"
     x-data="{
         paused: false,
         logLines: [],
         maxLines: 500,
         pendingLines: []
     }">
    <div class="flex items-center justify-between mb-4">
        <h2 class="font-semibold text-gray-900">Logs</h2>
        <div class="flex items-center gap-2">
            <button @click="paused = !paused; if (!paused) { logLines = logLines.concat(pendingLines); pendingLines = []; }"
                    :class="paused ? 'bg-yellow-100 text-yellow-700' : 'bg-gray-100 text-gray-600'"
                    class="px-2 py-1 text-xs rounded flex items-center gap-1">
                <template x-if="paused">
                    <svg class="w-3 h-3" fill="currentColor" viewBox="0 0 20 20">
                        <path d="M6.3 2.841A1.5 1.5 0 004 4.11V15.89a1.5 1.5 0 002.3 1.269l9.344-5.89a1.5 1.5 0 000-2.538L6.3 2.84z"/>
                    </svg>
                </template>
                <template x-if="!paused">
                    <svg class="w-3 h-3" fill="currentColor" viewBox="0 0 20 20">
                        <path d="M5.75 3a.75.75 0 00-.75.75v12.5c0 .414.336.75.75.75h1.5a.75.75 0 00.75-.75V3.75A.75.75 0 007.25 3h-1.5zM12.75 3a.75.75 0 00-.75.75v12.5c0 .414.336.75.75.75h1.5a.75.75 0 00.75-.75V3.75a.75.75 0 00-.75-.75h-1.5z"/>
                    </svg>
                </template>
                <span x-text="paused ? 'Resume' + (pendingLines.length > 0 ? ' (' + pendingLines.length + ')' : '') : 'Pause'"></span>
            </button>
        </div>
    </div>
    <!-- Log content with pause-aware updates -->
    <div class="bg-gray-900 text-gray-300 rounded-lg p-4 font-mono text-xs h-64 overflow-auto" id="logs">
        <template x-for="line in logLines" :key="line.id">
            <div :class="line.stream === 'stderr' ? 'text-red-400' : ''" x-text="line.text"></div>
        </template>
    </div>
</div>
```

**Step 2: Update WebSocket handler to respect pause state**

Add to the WebSocket message handler:

```javascript
ws.onmessage = (e) => {
    const data = JSON.parse(e.data);
    if (data.type === 'log') {
        const newLine = { id: Date.now() + Math.random(), stream: data.stream, text: data.line };
        if (paused) {
            pendingLines.push(newLine);
        } else {
            logLines.push(newLine);
            if (logLines.length > maxLines) logLines.shift();
        }
    }
};
```

**Step 3: Commit**

```bash
git add pkg/dashboard/templates/vm_detail.html
git commit -m "feat(dashboard): add log pause/resume controls"
```

---

## Task 2B: Add Historical Log Search

**Files:**
- Modify: `/home/jesse/git/stockyard/pkg/dashboard/templates/vm_detail.html`
- Create: `/home/jesse/git/stockyard/pkg/dashboard/log_history.go`
- Create: `/home/jesse/git/stockyard/pkg/dashboard/log_history_test.go`

**Step 1: Write failing test for log history storage**

Create `pkg/dashboard/log_history_test.go`:

```go
package dashboard

import (
	"testing"
)

func TestLogHistory_StoresLines(t *testing.T) {
	history := NewLogHistory(100)

	history.AddLine("task-1", "stdout", "line 1")
	history.AddLine("task-1", "stdout", "line 2")
	history.AddLine("task-1", "stderr", "error line")

	lines := history.Search("task-1", "line")
	if len(lines) != 3 {
		t.Errorf("expected 3 matching lines, got %d", len(lines))
	}
}

func TestLogHistory_FiltersStream(t *testing.T) {
	history := NewLogHistory(100)

	history.AddLine("task-1", "stdout", "out line")
	history.AddLine("task-1", "stderr", "err line")

	lines := history.SearchStream("task-1", "stderr", "")
	if len(lines) != 1 {
		t.Errorf("expected 1 stderr line, got %d", len(lines))
	}
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./pkg/dashboard/... -run TestLogHistory -v`
Expected: FAIL - LogHistory undefined

**Step 3: Write minimal implementation**

Create `pkg/dashboard/log_history.go`:

```go
package dashboard

import (
	"strings"
	"sync"
	"time"
)

// LogLine represents a stored log line.
type LogLine struct {
	TaskID    string    `json:"task_id"`
	Stream    string    `json:"stream"`
	Text      string    `json:"text"`
	Timestamp time.Time `json:"timestamp"`
}

// LogHistory stores recent log lines for search.
type LogHistory struct {
	lines    []LogLine
	maxLines int
	mu       sync.RWMutex
}

// NewLogHistory creates a new log history store.
func NewLogHistory(maxLines int) *LogHistory {
	return &LogHistory{
		lines:    make([]LogLine, 0, maxLines),
		maxLines: maxLines,
	}
}

// AddLine stores a log line.
func (lh *LogHistory) AddLine(taskID, stream, text string) {
	lh.mu.Lock()
	defer lh.mu.Unlock()

	lh.lines = append(lh.lines, LogLine{
		TaskID:    taskID,
		Stream:    stream,
		Text:      text,
		Timestamp: time.Now(),
	})

	if len(lh.lines) > lh.maxLines {
		lh.lines = lh.lines[1:]
	}
}

// Search finds lines matching a query string.
func (lh *LogHistory) Search(taskID, query string) []LogLine {
	lh.mu.RLock()
	defer lh.mu.RUnlock()

	var results []LogLine
	query = strings.ToLower(query)
	for _, line := range lh.lines {
		if line.TaskID == taskID {
			if query == "" || strings.Contains(strings.ToLower(line.Text), query) {
				results = append(results, line)
			}
		}
	}
	return results
}

// SearchStream finds lines matching query in a specific stream.
func (lh *LogHistory) SearchStream(taskID, stream, query string) []LogLine {
	lh.mu.RLock()
	defer lh.mu.RUnlock()

	var results []LogLine
	query = strings.ToLower(query)
	for _, line := range lh.lines {
		if line.TaskID == taskID && line.Stream == stream {
			if query == "" || strings.Contains(strings.ToLower(line.Text), query) {
				results = append(results, line)
			}
		}
	}
	return results
}
```

**Step 4: Run test to verify it passes**

Run: `go test ./pkg/dashboard/... -run TestLogHistory -v`
Expected: PASS

**Step 5: Add log search API endpoint**

Add to `server.go`:

```go
func (s *Server) registerRoutes() {
	// ... existing routes
	s.mux.HandleFunc("/api/vm/", s.handleAPIVM)
	s.mux.HandleFunc("/api/vm-logs/", s.handleLogSearch)  // Add this
}

func (s *Server) handleLogSearch(w http.ResponseWriter, r *http.Request) {
	// /api/vm-logs/task-123?q=error&stream=stderr
	id := strings.TrimPrefix(r.URL.Path, "/api/vm-logs/")
	if id == "" {
		http.Error(w, "missing task ID", http.StatusBadRequest)
		return
	}

	query := r.URL.Query().Get("q")
	stream := r.URL.Query().Get("stream")

	var lines []LogLine
	if stream != "" && stream != "all" {
		lines = s.logHistory.SearchStream(id, stream, query)
	} else {
		lines = s.logHistory.Search(id, query)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(lines)
}
```

**Step 6: Add search UI to template**

Add search input to log panel:

```html
<div class="flex items-center gap-2 mb-4"
     x-data="{ searchQuery: '', searchResults: [], searching: false }">
    <input type="text" x-model="searchQuery"
           @input.debounce.300ms="
               if (searchQuery.length >= 2) {
                   searching = true;
                   fetch('/api/vm-logs/{{.Task.ID}}?q=' + encodeURIComponent(searchQuery))
                       .then(r => r.json())
                       .then(data => { searchResults = data; searching = false; });
               } else {
                   searchResults = [];
               }
           "
           placeholder="Search historical logs..."
           class="flex-1 px-3 py-1 text-sm border border-gray-300 rounded focus:ring-2 focus:ring-blue-500 focus:border-blue-500">
    <span x-show="searching" class="text-xs text-gray-400">Searching...</span>
    <button @click="searchQuery = ''; searchResults = []"
            x-show="searchQuery"
            class="px-2 py-1 text-xs text-gray-500 hover:text-gray-700">
        Clear
    </button>
</div>
<!-- Search results -->
<div x-show="searchResults.length > 0" class="mb-2 text-xs text-gray-500">
    Found <span x-text="searchResults.length"></span> matches
</div>
```

**Step 7: Commit**

```bash
git add pkg/dashboard/log_history.go pkg/dashboard/log_history_test.go pkg/dashboard/server.go pkg/dashboard/templates/vm_detail.html
git commit -m "feat(dashboard): add historical log search"
```

---

## Task 2C: Add Log Filter Input

**Files:**
- Modify: `/home/jesse/git/stockyard/pkg/dashboard/templates/vm_detail.html`

**Step 1: Add filter state and UI**

Extend the log panel with real-time filtering:

```html
<div class="flex items-center gap-2 mb-2">
    <input type="text" x-model="filterText"
           placeholder="Filter current logs..."
           class="flex-1 px-3 py-1 text-sm border border-gray-300 rounded focus:ring-2 focus:ring-blue-500">
    <select x-model="filterStream" class="px-2 py-1 text-sm border border-gray-300 rounded">
        <option value="all">All</option>
        <option value="stdout">stdout</option>
        <option value="stderr">stderr</option>
    </select>
</div>

<!-- Filtered log display -->
<div class="bg-gray-900 text-gray-300 rounded-lg p-4 font-mono text-xs h-64 overflow-auto">
    <template x-for="line in filteredLines" :key="line.id">
        <div :class="line.stream === 'stderr' ? 'text-red-400' : ''" x-text="line.text"></div>
    </template>
</div>
```

**Step 2: Add computed property for filtered lines**

```javascript
// Add to x-data
filterText: '',
filterStream: 'all',

// Add computed getter
get filteredLines() {
    return this.logLines.filter(line => {
        const matchesStream = this.filterStream === 'all' || line.stream === this.filterStream;
        const matchesText = !this.filterText || line.text.toLowerCase().includes(this.filterText.toLowerCase());
        return matchesStream && matchesText;
    });
}
```

**Step 3: Commit**

```bash
git add pkg/dashboard/templates/vm_detail.html
git commit -m "feat(dashboard): add log filter input"
```

---

## Task 3: Implement Split View Layout

**Files:**
- Modify: `/home/jesse/git/stockyard/pkg/dashboard/templates/fleet.html`
- Modify: `/home/jesse/git/stockyard/pkg/dashboard/server.go`

**Step 1: Update fleet template with split view**

Replace the main content area in `fleet.html`:

```html
<!-- Main content with split view -->
<main class="flex-1 overflow-hidden flex">
    <!-- Left panel: VM list -->
    <div class="flex-1 overflow-auto p-6 border-r border-gray-200"
         :class="selectedVM ? 'w-3/5' : 'w-full'"
         x-data="{ selectedVM: null, previewData: null }">

        <div class="mb-6 flex items-center justify-between">
            <h1 class="text-2xl font-bold text-gray-900">Fleet Overview</h1>
            <span x-show="connected" class="text-xs text-green-600 flex items-center gap-1">
                <span class="w-2 h-2 bg-green-500 rounded-full animate-pulse"></span>
                Live
            </span>
        </div>

        {{if .GroupedTasks}}
        <div class="space-y-4">
            {{range $repo, $tasks := .GroupedTasks}}
            <div class="bg-white rounded-lg border border-gray-200 overflow-hidden" x-data="{ open: true }">
                <button @click="open = !open" class="w-full px-4 py-3 flex items-center justify-between bg-gray-50 hover:bg-gray-100">
                    <div class="flex items-center gap-2">
                        <svg :class="open ? 'rotate-90' : ''" class="w-4 h-4 transition-transform" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                            <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M9 5l7 7-7 7"/>
                        </svg>
                        <span class="font-medium">{{$repo}}</span>
                        <span class="text-sm text-gray-500">({{len $tasks}} VMs)</span>
                    </div>
                    <div class="flex items-center gap-2">
                        {{range $tasks}}
                        {{if eq .Status "running"}}<span class="w-2 h-2 bg-green-500 rounded-full"></span>{{end}}
                        {{if eq .Status "failed"}}<span class="w-2 h-2 bg-red-500 rounded-full"></span>{{end}}
                        {{end}}
                    </div>
                </button>
                <div x-show="open" x-collapse>
                    <div class="divide-y divide-gray-100">
                        {{range $tasks}}
                        <div class="px-4 py-3 flex items-center justify-between hover:bg-gray-50 cursor-pointer"
                             @click="selectedVM = '{{.ID}}'; $dispatch('select-vm', { id: '{{.ID}}' })"
                             :class="selectedVM === '{{.ID}}' ? 'bg-blue-50 border-l-2 border-blue-500' : ''">
                            <div class="flex items-center gap-3">
                                {{if eq .Status "running"}}
                                <span class="w-2 h-2 bg-green-500 rounded-full"></span>
                                {{else if eq .Status "stopped"}}
                                <span class="w-2 h-2 bg-gray-400 rounded-full"></span>
                                {{else if eq .Status "failed"}}
                                <span class="w-2 h-2 bg-red-500 rounded-full"></span>
                                {{end}}
                                <div>
                                    <div class="font-medium text-gray-900">{{.ID}}</div>
                                    <div class="text-sm text-gray-500">{{.Name}}</div>
                                </div>
                            </div>
                            <div class="flex items-center gap-4 text-sm text-gray-500">
                                <span>{{.Owner}}</span>
                                <span class="tabular-nums" x-text="metrics['{{.ID}}']?.cpu ? metrics['{{.ID}}'].cpu.toFixed(0) + '%' : '--'">--</span>
                            </div>
                        </div>
                        {{end}}
                    </div>
                </div>
            </div>
            {{end}}
        </div>
        {{else}}
        <div class="bg-white rounded-lg border border-gray-200 p-8 text-center text-gray-500">
            <p>No VMs running.</p>
            <p class="text-sm mt-2">Create one from the CLI with <code class="bg-gray-100 px-2 py-1 rounded">stockyard run</code></p>
        </div>
        {{end}}
    </div>

    <!-- Right panel: Preview -->
    <div x-show="selectedVM"
         x-transition:enter="transition ease-out duration-200"
         x-transition:enter-start="opacity-0 translate-x-4"
         x-transition:enter-end="opacity-100 translate-x-0"
         class="w-2/5 bg-gray-50 overflow-auto p-6 hidden lg:block"
         id="preview-panel">
        <div x-html="previewContent"></div>
    </div>
</main>
```

**Step 2: Add preview API endpoint**

Add to `server.go`:

```go
func (s *Server) registerRoutes() {
	s.mux.HandleFunc("/health", s.handleHealth)
	s.mux.HandleFunc("/ws", s.handleWebSocket)
	s.mux.HandleFunc("/api/vm/", s.handleAPIVM)
	s.mux.HandleFunc("/preview/vm/", s.handleVMPreview)  // Add this
	s.mux.HandleFunc("/vm/", s.handleVMDetail)
	s.mux.HandleFunc("/", s.handleFleet)
}

func (s *Server) handleVMPreview(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/preview/vm/")
	if id == "" {
		http.NotFound(w, r)
		return
	}

	ctx := r.Context()
	task, err := s.daemon.GetTask(ctx, id)
	if err != nil || task == nil {
		http.NotFound(w, r)
		return
	}

	snapshots, _ := s.daemon.ListSnapshots(ctx, id)

	data := map[string]interface{}{
		"Task":      task,
		"Snapshots": snapshots,
	}

	w.Header().Set("Content-Type", "text/html")
	if err := s.templates.ExecuteTemplate(w, "vm_preview.html", data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}
```

**Step 3: Create preview template**

Create `pkg/dashboard/templates/vm_preview.html`:

```html
<div class="space-y-4">
    <div class="flex items-center justify-between">
        <div>
            <h2 class="text-lg font-bold text-gray-900">{{.Task.ID}}</h2>
            <p class="text-sm text-gray-500">{{.Task.RepoURL}}</p>
        </div>
        {{if eq .Task.Status "running"}}
        <span class="inline-flex items-center gap-1 px-2 py-1 bg-green-100 text-green-700 rounded-full text-sm">
            <span class="w-2 h-2 bg-green-500 rounded-full"></span>
            running
        </span>
        {{else if eq .Task.Status "stopped"}}
        <span class="inline-flex items-center gap-1 px-2 py-1 bg-gray-100 text-gray-600 rounded-full text-sm">
            <span class="w-2 h-2 bg-gray-400 rounded-full"></span>
            stopped
        </span>
        {{end}}
    </div>

    <!-- Quick metrics -->
    <div class="grid grid-cols-2 gap-2">
        <div class="p-3 bg-white rounded-lg border border-gray-200 text-center">
            <div class="text-lg font-bold text-gray-900" id="preview-cpu">--</div>
            <div class="text-xs text-gray-500">CPU</div>
        </div>
        <div class="p-3 bg-white rounded-lg border border-gray-200 text-center">
            <div class="text-lg font-bold text-gray-900" id="preview-mem">--</div>
            <div class="text-xs text-gray-500">Memory</div>
        </div>
    </div>

    <!-- Recent logs -->
    <div class="bg-gray-900 text-gray-300 rounded-lg p-3 font-mono text-xs h-32 overflow-auto" id="preview-logs">
        <p class="text-gray-500">Connecting...</p>
    </div>

    <!-- Actions -->
    <div class="flex gap-2">
        <button onclick="copySSH('{{.Task.TailscaleHost}}')"
                class="flex-1 px-3 py-2 bg-blue-600 text-white rounded-lg hover:bg-blue-700 text-sm">
            Copy SSH
        </button>
        <a href="/vm/{{.Task.ID}}"
           class="flex-1 px-3 py-2 bg-gray-200 text-gray-700 rounded-lg hover:bg-gray-300 text-sm text-center">
            Full Details
        </a>
    </div>
</div>
```

**Step 4: Add htmx loading for preview**

Update fleet.html to load preview via htmx:

```html
<div x-show="selectedVM"
     class="w-2/5 bg-gray-50 overflow-auto p-6 hidden lg:block"
     hx-get="/preview/vm/"
     hx-trigger="select-vm from:body"
     hx-vals='js:{"id": event.detail.id}'
     hx-target="this"
     hx-swap="innerHTML">
    <p class="text-gray-500 text-center">Select a VM to preview</p>
</div>
```

**Step 5: Commit**

```bash
git add pkg/dashboard/templates/fleet.html pkg/dashboard/templates/vm_preview.html pkg/dashboard/server.go
git commit -m "feat(dashboard): add split view layout with VM preview"
```

---

## Task 3A: Add "By Repo" and "By Owner" Tabs

**Files:**
- Modify: `/home/jesse/git/stockyard/pkg/dashboard/templates/fleet.html`
- Modify: `/home/jesse/git/stockyard/pkg/dashboard/server.go`

**Step 1: Update fleet handler to provide grouped data**

Add grouped data preparation to `handleFleet()` in `server.go`:

```go
func (s *Server) handleFleet(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	tasks, err := s.daemon.ListTasks(ctx, "")
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// Group by repo
	byRepo := make(map[string][]*DaemonTask)
	for _, t := range tasks {
		repo := t.Repo
		if repo == "" {
			repo = "(none)"
		}
		byRepo[repo] = append(byRepo[repo], t)
	}

	// Group by owner
	byOwner := make(map[string][]*DaemonTask)
	for _, t := range tasks {
		owner := t.Owner
		if owner == "" {
			owner = "(unknown)"
		}
		byOwner[owner] = append(byOwner[owner], t)
	}

	data := map[string]interface{}{
		"Title":        "Fleet",
		"User":         GetUser(r.Context()),
		"ActiveNav":    "fleet",
		"Tasks":        tasks,
		"GroupedByRepo": byRepo,
		"GroupedByOwner": byOwner,
	}

	// ... rest of handler
}
```

**Step 2: Add view tabs to fleet template**

Add tabs at the top of the VM list section:

```html
<div class="mb-6">
    <div class="flex gap-4 border-b border-gray-200">
        <button @click="viewTab = 'all'"
                :class="viewTab === 'all' ? 'border-b-2 border-blue-500 text-blue-600' : 'text-gray-500'"
                class="pb-2 px-1 text-sm font-medium">
            All VMs
        </button>
        <button @click="viewTab = 'repo'"
                :class="viewTab === 'repo' ? 'border-b-2 border-blue-500 text-blue-600' : 'text-gray-500'"
                class="pb-2 px-1 text-sm font-medium">
            By Repo
        </button>
        <button @click="viewTab = 'owner'"
                :class="viewTab === 'owner' ? 'border-b-2 border-blue-500 text-blue-600' : 'text-gray-500'"
                class="pb-2 px-1 text-sm font-medium">
            By Owner
        </button>
        <button @click="viewTab = 'running'"
                :class="viewTab === 'running' ? 'border-b-2 border-green-500 text-green-600' : 'text-gray-500'"
                class="pb-2 px-1 text-sm font-medium">
            Running
        </button>
        <button @click="viewTab = 'alerts'"
                :class="viewTab === 'alerts' ? 'border-b-2 border-red-500 text-red-600' : 'text-gray-500'"
                class="pb-2 px-1 text-sm font-medium flex items-center gap-1">
            Alerts
            {{if .AlertCount}}
            <span class="bg-red-100 text-red-700 text-xs px-1.5 py-0.5 rounded-full">{{.AlertCount}}</span>
            {{end}}
        </button>
    </div>
</div>

<!-- By Repo view -->
<div x-show="viewTab === 'repo'" class="space-y-4">
    {{range $repo, $tasks := .GroupedByRepo}}
    <div class="bg-white rounded-lg border border-gray-200 overflow-hidden" x-data="{ open: true }">
        <button @click="open = !open" class="w-full px-4 py-3 flex items-center justify-between bg-gray-50 hover:bg-gray-100">
            <div class="flex items-center gap-2">
                <svg :class="open ? 'rotate-90' : ''" class="w-4 h-4 transition-transform" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                    <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M9 5l7 7-7 7"/>
                </svg>
                <span class="font-medium truncate">{{$repo}}</span>
                <span class="text-sm text-gray-500">({{len $tasks}})</span>
            </div>
        </button>
        <div x-show="open" x-collapse>
            <div class="divide-y divide-gray-100">
                {{range $tasks}}
                <!-- VM row - same as in Task 3 -->
                {{end}}
            </div>
        </div>
    </div>
    {{end}}
</div>

<!-- By Owner view -->
<div x-show="viewTab === 'owner'" class="space-y-4">
    {{range $owner, $tasks := .GroupedByOwner}}
    <div class="bg-white rounded-lg border border-gray-200 overflow-hidden" x-data="{ open: true }">
        <button @click="open = !open" class="w-full px-4 py-3 flex items-center justify-between bg-gray-50 hover:bg-gray-100">
            <div class="flex items-center gap-2">
                <svg :class="open ? 'rotate-90' : ''" class="w-4 h-4 transition-transform" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                    <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M9 5l7 7-7 7"/>
                </svg>
                <div class="flex items-center gap-2">
                    <div class="w-6 h-6 rounded-full bg-blue-500 flex items-center justify-center text-white text-xs font-bold uppercase">
                        {{slice $owner 0 1}}
                    </div>
                    <span class="font-medium">{{$owner}}</span>
                </div>
                <span class="text-sm text-gray-500">({{len $tasks}})</span>
            </div>
        </button>
        <div x-show="open" x-collapse>
            <div class="divide-y divide-gray-100">
                {{range $tasks}}
                <!-- VM row - same as in Task 3 -->
                {{end}}
            </div>
        </div>
    </div>
    {{end}}
</div>
```

**Step 3: Add viewTab to x-data**

Update the x-data initialization:

```html
x-data="{
    selectedVM: null,
    showActivity: false,
    showSidebar: window.innerWidth >= 1024,
    viewTab: 'all',
    metrics: {},
    alerts: {},
    connected: false
}"
```

**Step 4: Commit**

```bash
git add pkg/dashboard/templates/fleet.html pkg/dashboard/server.go
git commit -m "feat(dashboard): add By Repo and By Owner view tabs"
```

---

## Task 3B: Add Double-Click to Open Detail

**Files:**
- Modify: `/home/jesse/git/stockyard/pkg/dashboard/templates/fleet.html`

**Step 1: Add double-click handler to VM rows**

Update the VM row in the fleet template:

```html
<div class="px-4 py-3 flex items-center justify-between hover:bg-gray-50 cursor-pointer"
     @click="selectedVM = '{{.ID}}'; $dispatch('select-vm', { id: '{{.ID}}' })"
     @dblclick="window.location.href = '/vm/{{.ID}}'"
     :class="selectedVM === '{{.ID}}' ? 'bg-blue-50 border-l-2 border-blue-500' : ''">
    <!-- ... existing content ... -->
</div>
```

**Step 2: Add visual hint for double-click**

Add a tooltip or small hint:

```html
<div class="text-xs text-gray-400 mt-1 hidden lg:block">
    Click to preview • Double-click to open
</div>
```

**Step 3: Commit**

```bash
git add pkg/dashboard/templates/fleet.html
git commit -m "feat(dashboard): add double-click to open VM detail"
```

---

## Task 3C: Add Sparklines to Preview Panel

**Files:**
- Modify: `/home/jesse/git/stockyard/pkg/dashboard/templates/vm_preview.html`

**Step 1: Add mini sparkline charts to preview**

Update the preview panel quick metrics section:

```html
<!-- Quick metrics with sparklines -->
<div class="grid grid-cols-2 gap-2">
    <div class="p-3 bg-white rounded-lg border border-gray-200">
        <div class="flex items-center justify-between mb-1">
            <span class="text-xs text-gray-500">CPU</span>
            <span class="text-sm font-bold text-gray-900" id="preview-cpu">--</span>
        </div>
        <div class="h-8">
            <canvas id="preview-cpu-sparkline"></canvas>
        </div>
    </div>
    <div class="p-3 bg-white rounded-lg border border-gray-200">
        <div class="flex items-center justify-between mb-1">
            <span class="text-xs text-gray-500">Memory</span>
            <span class="text-sm font-bold text-gray-900" id="preview-mem">--</span>
        </div>
        <div class="h-8">
            <canvas id="preview-mem-sparkline"></canvas>
        </div>
    </div>
</div>

<script>
(function() {
    const cpuData = [];
    const memData = [];

    const createMiniSparkline = (id, color) => {
        const ctx = document.getElementById(id);
        if (!ctx) return null;
        return new Chart(ctx, {
            type: 'line',
            data: { labels: [], datasets: [{ data: [], borderColor: color, backgroundColor: 'transparent', tension: 0.4, pointRadius: 0, borderWidth: 1 }] },
            options: { responsive: true, maintainAspectRatio: false, plugins: { legend: { display: false }, tooltip: { enabled: false } }, scales: { x: { display: false }, y: { display: false, min: 0, max: 100 } }, animation: false }
        });
    };

    const cpuChart = createMiniSparkline('preview-cpu-sparkline', '#10b981');
    const memChart = createMiniSparkline('preview-mem-sparkline', '#3b82f6');

    // Update from parent's WebSocket metrics
    window.addEventListener('preview-metrics', (e) => {
        if (e.detail.taskId === '{{.Task.ID}}') {
            const m = e.detail.metrics;
            document.getElementById('preview-cpu').textContent = m.cpu_percent.toFixed(0) + '%';
            document.getElementById('preview-mem').textContent = formatBytes(m.memory_bytes);

            cpuData.push(m.cpu_percent);
            if (cpuData.length > 20) cpuData.shift();
            if (cpuChart) {
                cpuChart.data.labels = cpuData.map((_, i) => i);
                cpuChart.data.datasets[0].data = cpuData;
                cpuChart.update('none');
            }

            const memPct = (m.memory_bytes / m.memory_max_bytes) * 100;
            memData.push(memPct);
            if (memData.length > 20) memData.shift();
            if (memChart) {
                memChart.data.labels = memData.map((_, i) => i);
                memChart.data.datasets[0].data = memData;
                memChart.update('none');
            }
        }
    });
})();
</script>
```

**Step 2: Dispatch metrics events from main page**

Add to fleet.html WebSocket handler:

```javascript
ws.onmessage = (e) => {
    const data = JSON.parse(e.data);
    if (data.type === 'metrics') {
        metrics[data.task_id] = data.metrics;
        // Dispatch for preview panel
        window.dispatchEvent(new CustomEvent('preview-metrics', {
            detail: { taskId: data.task_id, metrics: data.metrics }
        }));
    }
};
```

**Step 3: Commit**

```bash
git add pkg/dashboard/templates/vm_preview.html pkg/dashboard/templates/fleet.html
git commit -m "feat(dashboard): add sparklines to preview panel"
```

---

## Task 4: Add Activity Feed Infrastructure

**Files:**
- Create: `/home/jesse/git/stockyard/pkg/dashboard/activity.go`
- Create: `/home/jesse/git/stockyard/pkg/dashboard/activity_test.go`

**Step 1: Write the failing test**

Create `pkg/dashboard/activity_test.go`:

```go
package dashboard

import (
	"testing"
	"time"
)

func TestActivityFeed_RecordsEvents(t *testing.T) {
	feed := NewActivityFeed(100)

	feed.RecordEvent(ActivityEvent{
		Type:    "vm_started",
		TaskID:  "task-1",
		Message: "VM started",
	})

	events := feed.GetRecent(10)
	if len(events) != 1 {
		t.Errorf("expected 1 event, got %d", len(events))
	}

	if events[0].Type != "vm_started" {
		t.Errorf("expected vm_started, got %s", events[0].Type)
	}
}

func TestActivityFeed_LimitsSize(t *testing.T) {
	feed := NewActivityFeed(5)

	for i := 0; i < 10; i++ {
		feed.RecordEvent(ActivityEvent{
			Type:    "test",
			Message: "Event",
		})
	}

	events := feed.GetRecent(100)
	if len(events) != 5 {
		t.Errorf("expected 5 events (max), got %d", len(events))
	}
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./pkg/dashboard/... -run TestActivityFeed -v`
Expected: FAIL - ActivityFeed undefined

**Step 3: Write minimal implementation**

Create `pkg/dashboard/activity.go`:

```go
package dashboard

import (
	"encoding/json"
	"sync"
	"time"
)

// ActivityEvent represents an event in the activity feed.
type ActivityEvent struct {
	ID        string    `json:"id"`
	Type      string    `json:"type"`      // vm_started, vm_stopped, vm_failed, snapshot_created, etc.
	TaskID    string    `json:"task_id"`
	TaskName  string    `json:"task_name"`
	RepoURL   string    `json:"repo_url"`
	Owner     string    `json:"owner"`
	Message   string    `json:"message"`
	Timestamp time.Time `json:"timestamp"`
}

// ActivityFeed maintains a rolling log of recent events.
type ActivityFeed struct {
	events   []ActivityEvent
	maxSize  int
	hub      *Hub
	mu       sync.RWMutex
	sequence int64
}

// NewActivityFeed creates a new activity feed.
func NewActivityFeed(maxSize int) *ActivityFeed {
	return &ActivityFeed{
		events:  make([]ActivityEvent, 0, maxSize),
		maxSize: maxSize,
	}
}

// NewActivityFeedWithHub creates an activity feed that broadcasts events.
func NewActivityFeedWithHub(maxSize int, hub *Hub) *ActivityFeed {
	af := NewActivityFeed(maxSize)
	af.hub = hub
	return af
}

// RecordEvent adds an event to the feed.
func (af *ActivityFeed) RecordEvent(event ActivityEvent) {
	af.mu.Lock()
	defer af.mu.Unlock()

	af.sequence++
	event.ID = formatSequence(af.sequence)
	if event.Timestamp.IsZero() {
		event.Timestamp = time.Now()
	}

	af.events = append(af.events, event)
	if len(af.events) > af.maxSize {
		af.events = af.events[1:]
	}

	// Broadcast to connected clients
	if af.hub != nil {
		msg := struct {
			Type  string        `json:"type"`
			Event ActivityEvent `json:"event"`
		}{
			Type:  "activity",
			Event: event,
		}
		if data, err := json.Marshal(msg); err == nil {
			af.hub.BroadcastAll(data)
		}
	}
}

// GetRecent returns the most recent events.
func (af *ActivityFeed) GetRecent(n int) []ActivityEvent {
	af.mu.RLock()
	defer af.mu.RUnlock()

	if n > len(af.events) {
		n = len(af.events)
	}

	// Return in reverse order (newest first)
	result := make([]ActivityEvent, n)
	for i := 0; i < n; i++ {
		result[i] = af.events[len(af.events)-1-i]
	}
	return result
}

// VMStarted records a VM start event.
func (af *ActivityFeed) VMStarted(taskID, taskName, repoURL, owner string) {
	af.RecordEvent(ActivityEvent{
		Type:     "vm_started",
		TaskID:   taskID,
		TaskName: taskName,
		RepoURL:  repoURL,
		Owner:    owner,
		Message:  "VM started",
	})
}

// VMStopped records a VM stop event.
func (af *ActivityFeed) VMStopped(taskID, taskName string) {
	af.RecordEvent(ActivityEvent{
		Type:     "vm_stopped",
		TaskID:   taskID,
		TaskName: taskName,
		Message:  "VM stopped",
	})
}

// VMFailed records a VM failure event.
func (af *ActivityFeed) VMFailed(taskID, taskName, reason string) {
	af.RecordEvent(ActivityEvent{
		Type:     "vm_failed",
		TaskID:   taskID,
		TaskName: taskName,
		Message:  "VM failed: " + reason,
	})
}

// SnapshotCreated records a snapshot creation event.
func (af *ActivityFeed) SnapshotCreated(taskID, snapshotName, label string) {
	af.RecordEvent(ActivityEvent{
		Type:    "snapshot_created",
		TaskID:  taskID,
		Message: "Snapshot created: " + snapshotName,
	})
}

func formatSequence(seq int64) string {
	// Simple ID format
	return time.Now().Format("20060102150405") + "-" + string(rune(seq%26+'a'))
}
```

**Step 4: Run test to verify it passes**

Run: `go test ./pkg/dashboard/... -run TestActivityFeed -v`
Expected: PASS

**Step 5: Commit**

```bash
git add pkg/dashboard/activity.go pkg/dashboard/activity_test.go
git commit -m "feat(dashboard): add activity feed infrastructure"
```

---

## Task 5: Add Activity Feed UI

**Files:**
- Create: `/home/jesse/git/stockyard/pkg/dashboard/templates/activity_panel.html`
- Modify: `/home/jesse/git/stockyard/pkg/dashboard/templates/fleet.html`

**Step 1: Create activity panel template**

Create `pkg/dashboard/templates/activity_panel.html`:

```html
<div class="h-full flex flex-col" x-data="{ events: [], filter: 'all' }">
    <div class="flex items-center justify-between p-4 border-b border-gray-200">
        <h2 class="font-semibold text-gray-900">Activity</h2>
        <button @click="$dispatch('close-activity')" class="p-1 hover:bg-gray-100 rounded">
            <svg class="w-5 h-5" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M6 18L18 6M6 6l12 12"/>
            </svg>
        </button>
    </div>

    <div class="flex gap-2 p-4 border-b border-gray-200">
        <button @click="filter = 'all'"
                :class="filter === 'all' ? 'bg-blue-100 text-blue-700' : 'bg-gray-100'"
                class="px-2 py-1 text-xs rounded">All</button>
        <button @click="filter = 'started'"
                :class="filter === 'started' ? 'bg-green-100 text-green-700' : 'bg-gray-100'"
                class="px-2 py-1 text-xs rounded">Started</button>
        <button @click="filter = 'stopped'"
                :class="filter === 'stopped' ? 'bg-gray-200 text-gray-700' : 'bg-gray-100'"
                class="px-2 py-1 text-xs rounded">Stopped</button>
        <button @click="filter = 'failed'"
                :class="filter === 'failed' ? 'bg-red-100 text-red-700' : 'bg-gray-100'"
                class="px-2 py-1 text-xs rounded">Failed</button>
    </div>

    <div class="flex-1 overflow-auto">
        {{range .Events}}
        <div class="px-4 py-3 border-b border-gray-100 hover:bg-gray-50"
             x-show="filter === 'all' || filter === '{{.Type}}'.replace('vm_', '')">
            <div class="flex items-start gap-3">
                {{if eq .Type "vm_started"}}
                <span class="w-2 h-2 mt-2 bg-green-500 rounded-full"></span>
                {{else if eq .Type "vm_stopped"}}
                <span class="w-2 h-2 mt-2 bg-gray-400 rounded-full"></span>
                {{else if eq .Type "vm_failed"}}
                <span class="w-2 h-2 mt-2 bg-red-500 rounded-full"></span>
                {{else}}
                <span class="w-2 h-2 mt-2 bg-blue-500 rounded-full"></span>
                {{end}}
                <div class="flex-1 min-w-0">
                    <div class="flex items-center justify-between">
                        <a href="/vm/{{.TaskID}}" class="font-medium text-gray-900 hover:text-blue-600 truncate">
                            {{.TaskID}}
                        </a>
                        <span class="text-xs text-gray-400 shrink-0">
                            {{.Timestamp.Format "15:04"}}
                        </span>
                    </div>
                    <p class="text-sm text-gray-600">{{.Message}}</p>
                    {{if .RepoURL}}
                    <p class="text-xs text-gray-400 truncate">{{.RepoURL}}</p>
                    {{end}}
                </div>
            </div>
        </div>
        {{else}}
        <div class="p-4 text-center text-gray-500">
            No activity yet
        </div>
        {{end}}
    </div>
</div>
```

**Step 2: Add activity button and slide-over to fleet template**

Add to the header in `fleet.html`:

```html
<div class="flex items-center gap-4">
    <button @click="showActivity = !showActivity"
            class="p-2 hover:bg-gray-100 rounded relative">
        <svg class="w-5 h-5" fill="none" stroke="currentColor" viewBox="0 0 24 24">
            <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M15 17h5l-1.405-1.405A2.032 2.032 0 0118 14.158V11a6.002 6.002 0 00-4-5.659V5a2 2 0 10-4 0v.341C7.67 6.165 6 8.388 6 11v3.159c0 .538-.214 1.055-.595 1.436L4 17h5m6 0v1a3 3 0 11-6 0v-1m6 0H9"/>
        </svg>
        {{if .ActivityCount}}
        <span class="absolute -top-1 -right-1 bg-blue-500 text-white text-xs rounded-full w-5 h-5 flex items-center justify-center">
            {{.ActivityCount}}
        </span>
        {{end}}
    </button>
    <span class="text-sm text-gray-600">{{.User}}</span>
</div>
```

Add the slide-over panel before closing `</body>`:

```html
<!-- Activity slide-over -->
<div x-show="showActivity"
     x-transition:enter="transition ease-out duration-200"
     x-transition:enter-start="opacity-0"
     x-transition:enter-end="opacity-100"
     x-transition:leave="transition ease-in duration-150"
     x-transition:leave-start="opacity-100"
     x-transition:leave-end="opacity-0"
     class="fixed inset-0 z-40"
     @close-activity.window="showActivity = false">
    <!-- Backdrop -->
    <div class="absolute inset-0 bg-black bg-opacity-25" @click="showActivity = false"></div>

    <!-- Panel -->
    <div x-show="showActivity"
         x-transition:enter="transition ease-out duration-200"
         x-transition:enter-start="translate-x-full"
         x-transition:enter-end="translate-x-0"
         x-transition:leave="transition ease-in duration-150"
         x-transition:leave-start="translate-x-0"
         x-transition:leave-end="translate-x-full"
         class="absolute right-0 top-0 h-full w-96 bg-white shadow-xl">
        <div hx-get="/activity" hx-trigger="revealed" hx-swap="innerHTML" class="h-full">
            <div class="p-4 text-center text-gray-500">Loading...</div>
        </div>
    </div>
</div>
```

**Step 3: Add activity endpoint**

Add to `server.go`:

```go
func (s *Server) registerRoutes() {
	// ... existing routes
	s.mux.HandleFunc("/activity", s.handleActivity)
}

func (s *Server) handleActivity(w http.ResponseWriter, r *http.Request) {
	events := []ActivityEvent{} // TODO: Get from activity feed

	data := map[string]interface{}{
		"Events": events,
	}

	w.Header().Set("Content-Type", "text/html")
	if err := s.templates.ExecuteTemplate(w, "activity_panel.html", data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}
```

**Step 4: Commit**

```bash
git add pkg/dashboard/templates/activity_panel.html pkg/dashboard/templates/fleet.html pkg/dashboard/server.go
git commit -m "feat(dashboard): add activity feed slide-over panel"
```

---

## Task 5A: Add User Avatar to Header

**Files:**
- Modify: `/home/jesse/git/stockyard/pkg/dashboard/templates/fleet.html`
- Modify: `/home/jesse/git/stockyard/pkg/dashboard/templates/vm_detail.html`
- Modify: `/home/jesse/git/stockyard/pkg/dashboard/templates/layout.html` (if using shared layout)

**Step 1: Create avatar component**

Add a reusable avatar display that uses Tailscale user info:

```html
<!-- Avatar component - used in header -->
<div class="flex items-center gap-2">
    {{if .UserAvatar}}
    <img src="{{.UserAvatar}}" alt="{{.User}}" class="w-8 h-8 rounded-full">
    {{else}}
    <div class="w-8 h-8 rounded-full bg-blue-500 flex items-center justify-center text-white text-sm font-bold uppercase">
        {{slice .User 0 1}}
    </div>
    {{end}}
    <span class="text-sm text-gray-600 hidden sm:inline">{{.User}}</span>
</div>
```

**Step 2: Add avatar URL to context**

Update the Tailscale auth middleware to extract avatar URL:

```go
// In pkg/dashboard/auth.go or where user context is set
type UserInfo struct {
    Name      string
    LoginName string
    AvatarURL string
}

func getUserFromWhoIs(who *apitype.WhoIsResponse) UserInfo {
    user := UserInfo{
        Name:      who.UserProfile.DisplayName,
        LoginName: who.UserProfile.LoginName,
    }
    // Tailscale provides profile photo URL
    if who.UserProfile.ProfilePicURL != "" {
        user.AvatarURL = who.UserProfile.ProfilePicURL
    }
    return user
}
```

**Step 3: Update fleet template header**

Replace the plain text user display with avatar:

```html
<header class="bg-white border-b border-gray-200 px-4 py-2 flex items-center justify-between shrink-0">
    <div class="flex items-center gap-4">
        <!-- Mobile menu button -->
        <button @click="showSidebar = !showSidebar" class="lg:hidden p-2 hover:bg-gray-100 rounded">
            <svg class="w-5 h-5" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M4 6h16M4 12h16M4 18h16"/>
            </svg>
        </button>
        <span class="font-semibold text-lg">Stockyard</span>
    </div>

    <div class="flex items-center gap-4">
        <!-- Activity button -->
        <button @click="showActivity = !showActivity" class="p-2 hover:bg-gray-100 rounded relative">
            <svg class="w-5 h-5" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M15 17h5l-1.405-1.405A2.032 2.032 0 0118 14.158V11a6.002 6.002 0 00-4-5.659V5a2 2 0 10-4 0v.341C7.67 6.165 6 8.388 6 11v3.159c0 .538-.214 1.055-.595 1.436L4 17h5m6 0v1a3 3 0 11-6 0v-1m6 0H9"/>
            </svg>
        </button>

        <!-- User avatar -->
        <div class="flex items-center gap-2">
            {{if .UserAvatar}}
            <img src="{{.UserAvatar}}" alt="{{.User}}" class="w-8 h-8 rounded-full">
            {{else}}
            <div class="w-8 h-8 rounded-full bg-blue-500 flex items-center justify-center text-white text-sm font-bold uppercase">
                {{slice .User 0 1}}
            </div>
            {{end}}
            <span class="text-sm text-gray-600 hidden sm:inline">{{.User}}</span>
        </div>
    </div>
</header>
```

**Step 4: Commit**

```bash
git add pkg/dashboard/templates/fleet.html pkg/dashboard/templates/vm_detail.html pkg/dashboard/auth.go
git commit -m "feat(dashboard): add user avatar to header"
```

---

## Task 6: Add Alert Indicators

**Files:**
- Create: `/home/jesse/git/stockyard/pkg/dashboard/alerts.go`
- Create: `/home/jesse/git/stockyard/pkg/dashboard/alerts_test.go`

**Step 1: Write the failing test**

Create `pkg/dashboard/alerts_test.go`:

```go
package dashboard

import (
	"testing"
)

func TestAlertChecker_DetectsHighCPU(t *testing.T) {
	checker := NewAlertChecker()

	alerts := checker.Check("task-1", VMMetrics{
		CPUPercent:     95.0,
		MemoryBytes:    1024 * 1024 * 1024,
		MemoryMaxBytes: 4 * 1024 * 1024 * 1024,
	})

	if len(alerts) == 0 {
		t.Error("expected high CPU alert")
	}

	found := false
	for _, a := range alerts {
		if a.Type == "high_cpu" {
			found = true
		}
	}
	if !found {
		t.Error("expected high_cpu alert type")
	}
}

func TestAlertChecker_DetectsHighMemory(t *testing.T) {
	checker := NewAlertChecker()

	alerts := checker.Check("task-1", VMMetrics{
		CPUPercent:     50.0,
		MemoryBytes:    3900 * 1024 * 1024, // ~3.9GB
		MemoryMaxBytes: 4 * 1024 * 1024 * 1024, // 4GB (97.5% usage)
	})

	found := false
	for _, a := range alerts {
		if a.Type == "high_memory" {
			found = true
		}
	}
	if !found {
		t.Error("expected high_memory alert type")
	}
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./pkg/dashboard/... -run TestAlertChecker -v`
Expected: FAIL - AlertChecker undefined

**Step 3: Write minimal implementation**

Create `pkg/dashboard/alerts.go`:

```go
package dashboard

import (
	"sync"
	"time"
)

// Alert represents a problem condition.
type Alert struct {
	Type      string    `json:"type"`      // high_cpu, high_memory, unresponsive
	TaskID    string    `json:"task_id"`
	Severity  string    `json:"severity"`  // warning, critical
	Message   string    `json:"message"`
	Value     float64   `json:"value"`
	Threshold float64   `json:"threshold"`
	Since     time.Time `json:"since"`
}

// AlertChecker evaluates metrics and detects problems.
type AlertChecker struct {
	cpuThreshold      float64
	memoryThreshold   float64
	cpuDuration       time.Duration
	activeAlerts      map[string][]Alert
	highCPUSince      map[string]time.Time
	mu                sync.RWMutex
}

// NewAlertChecker creates a new alert checker with default thresholds.
func NewAlertChecker() *AlertChecker {
	return &AlertChecker{
		cpuThreshold:    90.0,  // 90% CPU
		memoryThreshold: 95.0,  // 95% memory
		cpuDuration:     5 * time.Minute,
		activeAlerts:    make(map[string][]Alert),
		highCPUSince:    make(map[string]time.Time),
	}
}

// Check evaluates metrics and returns any active alerts.
func (ac *AlertChecker) Check(taskID string, metrics VMMetrics) []Alert {
	ac.mu.Lock()
	defer ac.mu.Unlock()

	var alerts []Alert
	now := time.Now()

	// Check CPU
	if metrics.CPUPercent >= ac.cpuThreshold {
		if since, ok := ac.highCPUSince[taskID]; ok {
			if now.Sub(since) >= ac.cpuDuration {
				alerts = append(alerts, Alert{
					Type:      "high_cpu",
					TaskID:    taskID,
					Severity:  "warning",
					Message:   "CPU usage above 90% for 5+ minutes",
					Value:     metrics.CPUPercent,
					Threshold: ac.cpuThreshold,
					Since:     since,
				})
			}
		} else {
			ac.highCPUSince[taskID] = now
		}
	} else {
		delete(ac.highCPUSince, taskID)
	}

	// Check memory
	if metrics.MemoryMaxBytes > 0 {
		memPercent := float64(metrics.MemoryBytes) / float64(metrics.MemoryMaxBytes) * 100
		if memPercent >= ac.memoryThreshold {
			alerts = append(alerts, Alert{
				Type:      "high_memory",
				TaskID:    taskID,
				Severity:  "critical",
				Message:   "Memory usage above 95%",
				Value:     memPercent,
				Threshold: ac.memoryThreshold,
				Since:     now,
			})
		}
	}

	ac.activeAlerts[taskID] = alerts
	return alerts
}

// GetActiveAlerts returns all currently active alerts.
func (ac *AlertChecker) GetActiveAlerts() []Alert {
	ac.mu.RLock()
	defer ac.mu.RUnlock()

	var all []Alert
	for _, alerts := range ac.activeAlerts {
		all = append(all, alerts...)
	}
	return all
}

// GetAlertCount returns the total number of active alerts.
func (ac *AlertChecker) GetAlertCount() int {
	ac.mu.RLock()
	defer ac.mu.RUnlock()

	count := 0
	for _, alerts := range ac.activeAlerts {
		count += len(alerts)
	}
	return count
}

// ClearAlerts removes alerts for a task.
func (ac *AlertChecker) ClearAlerts(taskID string) {
	ac.mu.Lock()
	defer ac.mu.Unlock()
	delete(ac.activeAlerts, taskID)
	delete(ac.highCPUSince, taskID)
}
```

**Step 4: Run test to verify it passes**

Run: `go test ./pkg/dashboard/... -run TestAlertChecker -v`
Expected: PASS

**Step 5: Commit**

```bash
git add pkg/dashboard/alerts.go pkg/dashboard/alerts_test.go
git commit -m "feat(dashboard): add alert checking infrastructure"
```

---

## Task 6A: Add Unresponsive VM Detection

**Files:**
- Modify: `/home/jesse/git/stockyard/pkg/dashboard/alerts.go`
- Modify: `/home/jesse/git/stockyard/pkg/dashboard/alerts_test.go`

**Step 1: Write failing test for unresponsive detection**

Add to `alerts_test.go`:

```go
func TestAlertChecker_DetectsUnresponsive(t *testing.T) {
	checker := NewAlertChecker()
	// Override unresponsive threshold for faster testing
	checker.unresponsiveTime = 50 * time.Millisecond

	// First call - normal metrics
	_ = checker.Check("task-1", VMMetrics{
		CPUPercent:     10.0,
		MemoryBytes:    1024 * 1024 * 1024,
		MemoryMaxBytes: 4 * 1024 * 1024 * 1024,
	})

	// Wait for the unresponsive threshold to pass
	time.Sleep(100 * time.Millisecond)

	// Check again - should detect unresponsive
	alerts := checker.CheckUnresponsive("task-1")

	found := false
	for _, a := range alerts {
		if a.Type == "unresponsive" {
			found = true
		}
	}
	if !found {
		t.Error("expected unresponsive alert type")
	}
}

func TestAlertChecker_NoUnresponsiveForRecentMetrics(t *testing.T) {
	checker := NewAlertChecker()

	// Recent metrics
	_ = checker.Check("task-1", VMMetrics{
		CPUPercent:     10.0,
		MemoryBytes:    1024 * 1024 * 1024,
		MemoryMaxBytes: 4 * 1024 * 1024 * 1024,
	})

	// Check immediately - should not be unresponsive
	alerts := checker.CheckUnresponsive("task-1")

	for _, a := range alerts {
		if a.Type == "unresponsive" {
			t.Error("should not flag recent metrics as unresponsive")
		}
	}
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./pkg/dashboard/... -run TestAlertChecker_DetectsUnresponsive -v`
Expected: FAIL - lastMetricsTime undefined

**Step 3: Add unresponsive detection to AlertChecker**

Update `alerts.go`:

```go
type AlertChecker struct {
	cpuThreshold      float64
	memoryThreshold   float64
	cpuDuration       time.Duration
	unresponsiveTime  time.Duration
	activeAlerts      map[string][]Alert
	highCPUSince      map[string]time.Time
	lastMetricsTime   map[string]time.Time  // Track last metrics received
	mu                sync.RWMutex
}

func NewAlertChecker() *AlertChecker {
	return &AlertChecker{
		cpuThreshold:     90.0,
		memoryThreshold:  95.0,
		cpuDuration:      5 * time.Minute,
		unresponsiveTime: 60 * time.Second,  // No metrics for 60s = unresponsive
		activeAlerts:     make(map[string][]Alert),
		highCPUSince:     make(map[string]time.Time),
		lastMetricsTime:  make(map[string]time.Time),
	}
}

// Check evaluates metrics and returns any active alerts.
func (ac *AlertChecker) Check(taskID string, metrics VMMetrics) []Alert {
	ac.mu.Lock()
	defer ac.mu.Unlock()

	// Record that we received metrics
	ac.lastMetricsTime[taskID] = time.Now()

	// ... rest of existing Check implementation ...
}

// CheckUnresponsive checks if a VM hasn't reported metrics recently.
func (ac *AlertChecker) CheckUnresponsive(taskID string) []Alert {
	ac.mu.RLock()
	defer ac.mu.RUnlock()

	var alerts []Alert
	now := time.Now()

	if lastTime, ok := ac.lastMetricsTime[taskID]; ok {
		if now.Sub(lastTime) > ac.unresponsiveTime {
			alerts = append(alerts, Alert{
				Type:     "unresponsive",
				TaskID:   taskID,
				Severity: "critical",
				Message:  "VM not responding - no metrics received",
				Since:    lastTime,
			})
		}
	}

	return alerts
}

// CheckAllUnresponsive checks all known VMs for unresponsive state.
func (ac *AlertChecker) CheckAllUnresponsive() []Alert {
	ac.mu.RLock()
	taskIDs := make([]string, 0, len(ac.lastMetricsTime))
	for id := range ac.lastMetricsTime {
		taskIDs = append(taskIDs, id)
	}
	ac.mu.RUnlock()

	var alerts []Alert
	for _, id := range taskIDs {
		alerts = append(alerts, ac.CheckUnresponsive(id)...)
	}
	return alerts
}
```

**Step 4: Run test to verify it passes**

Run: `go test ./pkg/dashboard/... -run TestAlertChecker_DetectsUnresponsive -v`
Expected: PASS

**Step 5: Add unresponsive indicator to UI**

Add to the VM row in fleet.html:

```html
<!-- Alert indicators -->
<template x-if="alerts['{{.ID}}']?.some(a => a.type === 'unresponsive')">
    <span class="text-yellow-500" title="Unresponsive">
        <svg class="w-4 h-4" fill="currentColor" viewBox="0 0 20 20">
            <path fill-rule="evenodd" d="M18 10a8 8 0 11-16 0 8 8 0 0116 0zm-7 4a1 1 0 11-2 0 1 1 0 012 0zm-1-9a1 1 0 00-1 1v4a1 1 0 102 0V6a1 1 0 00-1-1z" clip-rule="evenodd"/>
        </svg>
    </span>
</template>
```

**Step 6: Commit**

```bash
git add pkg/dashboard/alerts.go pkg/dashboard/alerts_test.go pkg/dashboard/templates/fleet.html
git commit -m "feat(dashboard): add unresponsive VM detection"
```

---

## Task 7: Add Alert Badge to Fleet Page

**Files:**
- Modify: `/home/jesse/git/stockyard/pkg/dashboard/templates/fleet.html`
- Modify: `/home/jesse/git/stockyard/pkg/dashboard/server.go`

**Step 1: Update fleet handler to include alert count**

Modify `handleFleet()` in `server.go`:

```go
func (s *Server) handleFleet(w http.ResponseWriter, r *http.Request) {
	// ... existing code to get tasks ...

	alertCount := 0 // TODO: Get from alert checker

	data := map[string]interface{}{
		"Title":        "Fleet",
		"User":         GetUser(r.Context()),
		"ActiveNav":    "fleet",
		"Tasks":        tasks,
		"GroupedTasks": grouped,
		"AlertCount":   alertCount,
	}

	// ... rest of handler
}
```

**Step 2: Add alert filter tab to fleet page**

Update the tabs section in `fleet.html`:

```html
<!-- Tabs -->
<div class="flex gap-4 mb-6 border-b border-gray-200">
    <button @click="tab = 'all'"
            :class="tab === 'all' ? 'border-b-2 border-blue-500 text-blue-600' : 'text-gray-500'"
            class="pb-2 px-1 text-sm font-medium">
        All VMs
    </button>
    <button @click="tab = 'running'"
            :class="tab === 'running' ? 'border-b-2 border-blue-500 text-blue-600' : 'text-gray-500'"
            class="pb-2 px-1 text-sm font-medium">
        Running
    </button>
    <button @click="tab = 'alerts'"
            :class="tab === 'alerts' ? 'border-b-2 border-red-500 text-red-600' : 'text-gray-500'"
            class="pb-2 px-1 text-sm font-medium flex items-center gap-1">
        Alerts
        {{if .AlertCount}}
        <span class="bg-red-100 text-red-700 text-xs px-1.5 py-0.5 rounded-full">{{.AlertCount}}</span>
        {{end}}
    </button>
</div>
```

**Step 3: Highlight problem VMs**

Update the VM row display to show alert indicator:

```html
<div class="px-4 py-3 flex items-center justify-between hover:bg-gray-50 cursor-pointer"
     :class="{
         'bg-blue-50 border-l-2 border-blue-500': selectedVM === '{{.ID}}',
         'bg-red-50 border-l-2 border-red-500': alerts['{{.ID}}']?.length > 0
     }">
    <!-- ... existing content ... -->

    <!-- Alert indicator -->
    <template x-if="alerts['{{.ID}}']?.length > 0">
        <span class="text-red-500" title="Has alerts">
            <svg class="w-4 h-4" fill="currentColor" viewBox="0 0 20 20">
                <path fill-rule="evenodd" d="M8.257 3.099c.765-1.36 2.722-1.36 3.486 0l5.58 9.92c.75 1.334-.213 2.98-1.742 2.98H4.42c-1.53 0-2.493-1.646-1.743-2.98l5.58-9.92zM11 13a1 1 0 11-2 0 1 1 0 012 0zm-1-8a1 1 0 00-1 1v3a1 1 0 002 0V6a1 1 0 00-1-1z" clip-rule="evenodd"/>
            </svg>
        </span>
    </template>
</div>
```

**Step 4: Commit**

```bash
git add pkg/dashboard/templates/fleet.html pkg/dashboard/server.go
git commit -m "feat(dashboard): add alert indicators to fleet page"
```

---

## Task 8: Add Responsive Layout

**Files:**
- Modify: `/home/jesse/git/stockyard/pkg/dashboard/templates/fleet.html`
- Modify: `/home/jesse/git/stockyard/pkg/dashboard/templates/vm_detail.html`

**Step 1: Update fleet template for responsive design**

Add responsive classes to `fleet.html`:

```html
<body class="bg-gray-50 text-gray-900 h-screen flex flex-col"
      x-data="{
          selectedVM: null,
          showActivity: false,
          showSidebar: window.innerWidth >= 1024,
          tab: 'all',
          metrics: {},
          alerts: {},
          connected: false
      }"
      @resize.window="showSidebar = window.innerWidth >= 1024">

    <!-- Mobile header with hamburger -->
    <header class="bg-white border-b border-gray-200 px-4 py-2 flex items-center justify-between shrink-0">
        <div class="flex items-center gap-4">
            <!-- Mobile menu button -->
            <button @click="showSidebar = !showSidebar"
                    class="lg:hidden p-2 hover:bg-gray-100 rounded">
                <svg class="w-5 h-5" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                    <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M4 6h16M4 12h16M4 18h16"/>
                </svg>
            </button>
            <span class="font-semibold text-lg">Stockyard</span>
        </div>
        <!-- ... rest of header -->
    </header>

    <div class="flex flex-1 overflow-hidden">
        <!-- Sidebar - hidden on mobile unless toggled -->
        <nav x-show="showSidebar"
             x-transition:enter="transition ease-out duration-200"
             x-transition:enter-start="-translate-x-full"
             x-transition:enter-end="translate-x-0"
             x-transition:leave="transition ease-in duration-150"
             x-transition:leave-start="translate-x-0"
             x-transition:leave-end="-translate-x-full"
             class="fixed lg:relative inset-y-0 left-0 z-30 w-48 bg-white border-r border-gray-200 flex flex-col shrink-0 lg:translate-x-0"
             @click.away="if (window.innerWidth < 1024) showSidebar = false">
            <!-- Sidebar content -->
        </nav>

        <!-- Backdrop for mobile sidebar -->
        <div x-show="showSidebar && window.innerWidth < 1024"
             class="fixed inset-0 bg-black bg-opacity-25 z-20 lg:hidden"
             @click="showSidebar = false">
        </div>

        <!-- Main content -->
        <main class="flex-1 overflow-hidden flex flex-col lg:flex-row">
            <!-- VM list -->
            <div class="flex-1 overflow-auto p-4 lg:p-6"
                 :class="selectedVM && window.innerWidth >= 1024 ? 'lg:w-3/5' : 'w-full'">
                <!-- Content -->
            </div>

            <!-- Preview panel - only on large screens -->
            <div x-show="selectedVM"
                 class="hidden lg:block lg:w-2/5 bg-gray-50 overflow-auto p-6 border-l border-gray-200">
                <!-- Preview content -->
            </div>
        </main>
    </div>

    <!-- Mobile bottom nav -->
    <nav class="lg:hidden fixed bottom-0 inset-x-0 bg-white border-t border-gray-200 flex justify-around py-2 z-20">
        <a href="/" class="flex flex-col items-center p-2 text-blue-600">
            <svg class="w-6 h-6" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M4 6a2 2 0 012-2h2a2 2 0 012 2v2a2 2 0 01-2 2H6a2 2 0 01-2-2V6z"/>
            </svg>
            <span class="text-xs">Fleet</span>
        </a>
        <button @click="showActivity = true" class="flex flex-col items-center p-2 text-gray-600">
            <svg class="w-6 h-6" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M15 17h5l-1.405-1.405A2.032 2.032 0 0118 14.158V11a6.002 6.002 0 00-4-5.659V5a2 2 0 10-4 0v.341C7.67 6.165 6 8.388 6 11v3.159c0 .538-.214 1.055-.595 1.436L4 17h5m6 0v1a3 3 0 11-6 0v-1m6 0H9"/>
            </svg>
            <span class="text-xs">Activity</span>
        </button>
    </nav>

    <!-- Add padding for mobile bottom nav -->
    <div class="lg:hidden h-16"></div>
</body>
```

**Step 2: Update VM detail for responsive design**

Update `vm_detail.html`:

```html
<!-- Resources panel - responsive grid -->
<div class="grid grid-cols-2 lg:grid-cols-4 gap-2 lg:gap-4">
    <!-- Metric cards -->
</div>

<!-- Logs panel - full width on mobile -->
<div class="bg-gray-900 text-gray-300 rounded-lg p-4 font-mono text-xs sm:text-sm h-48 lg:h-64 overflow-auto">
    <!-- Log content -->
</div>

<!-- Details - responsive grid -->
<dl class="grid grid-cols-1 sm:grid-cols-2 gap-4">
    <!-- Detail items -->
</dl>

<!-- Actions - stack on mobile -->
<div class="flex flex-col sm:flex-row gap-2">
    <button class="flex-1 px-4 py-2 bg-blue-600 text-white rounded-lg">Copy SSH</button>
    <!-- More buttons -->
</div>
```

**Step 3: Test responsiveness**

Manually test at different viewport widths:
- Mobile (<768px): Single column, bottom nav
- Tablet (768-1024px): Collapsed sidebar, no split view
- Desktop (>1024px): Full sidebar, split view

**Step 4: Commit**

```bash
git add pkg/dashboard/templates/fleet.html pkg/dashboard/templates/vm_detail.html
git commit -m "feat(dashboard): add responsive layout for mobile and tablet"
```

---

## Task 9: Final Integration and Polish

**Files:**
- Modify: `/home/jesse/git/stockyard/pkg/daemon/daemon.go`

**Step 1: Wire all components together**

Update daemon to integrate activity feed and alert checker:

```go
type Daemon struct {
	// ... existing fields
	dashboardServer *dashboard.Server
	metricsPoller   *MetricsPoller
	activityFeed    *dashboard.ActivityFeed
	alertChecker    *dashboard.AlertChecker
}

func (d *Daemon) Start(ctx context.Context) error {
	// ... existing setup ...

	if d.cfg.HTTP.Enabled {
		d.dashboardServer = dashboard.NewServer(d)
		d.activityFeed = dashboard.NewActivityFeedWithHub(1000, d.dashboardServer.Hub())
		d.alertChecker = dashboard.NewAlertChecker()

		// Wire up metrics to check alerts
		metricsCollector := dashboard.NewMetricsCollector(d.dashboardServer.Hub())
		d.metricsPoller = NewMetricsPoller(d, &dashboardMetricsSinkWithAlerts{
			collector:    metricsCollector,
			alertChecker: d.alertChecker,
		}, 5*time.Second)
		d.metricsPoller.Start()

		// ... rest of HTTP setup
	}
}

type dashboardMetricsSinkWithAlerts struct {
	collector    *dashboard.MetricsCollector
	alertChecker *dashboard.AlertChecker
}

func (s *dashboardMetricsSinkWithAlerts) SendMetrics(taskID string, metrics interface{}) {
	if m, ok := metrics.(dashboard.VMMetrics); ok {
		s.collector.SendMetrics(taskID, m)
		s.alertChecker.Check(taskID, m)
	}
}
```

**Step 2: Add activity recording to task lifecycle**

Update task manager to record events:

```go
// In task creation
if d.activityFeed != nil {
	d.activityFeed.VMStarted(task.ID, task.Name, task.RepoURL, task.Owner)
}

// In task stop
if d.activityFeed != nil {
	d.activityFeed.VMStopped(task.ID, task.Name)
}

// In task failure
if d.activityFeed != nil {
	d.activityFeed.VMFailed(task.ID, task.Name, reason)
}
```

**Step 3: Run all tests**

Run: `go test ./pkg/dashboard/... ./pkg/daemon/... -v`
Expected: PASS

**Step 4: Commit**

```bash
git add pkg/daemon/daemon.go pkg/daemon/tasks.go
git commit -m "feat(daemon): integrate activity feed and alert checking"
```

---

## Summary

Phase 3 implementation adds:
- Chart.js sparklines for resource metrics
- Expandable full charts for detailed analysis
- Log pause/resume controls
- Historical log search
- Log filter input (real-time filtering)
- Split view layout with preview panel
- "By Repo" and "By Owner" view tabs
- Double-click to open VM detail
- Sparklines in preview panel
- Activity feed with slide-over panel
- Event recording for VM lifecycle
- User avatar display in header
- Alert detection for high CPU/memory
- Unresponsive VM detection
- Alert badges and indicators
- Responsive design for mobile/tablet

After Phase 3, the dashboard is fully polished with:
- Visual sparklines showing resource trends
- Complete log viewing controls (pause, search, filter)
- Multiple view modes (All, By Repo, By Owner, Running, Alerts)
- Quick preview without leaving fleet page
- Real-time activity feed
- User profile display with avatar
- Alert indicators for problem VMs (high CPU, high memory, unresponsive)
- Full functionality on any device size

Phase 4 (Terminal) adds xterm.js for in-browser SSH access.
