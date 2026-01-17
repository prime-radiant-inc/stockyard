# Stockyard Dashboard Design

## Overview

Web-based dashboard for managing a fleet of ephemeral VMs running autonomous coding agents. Provides observability, control, and debugging capabilities for human operators and developers.

## Users & Auth

- **Operators:** Full fleet visibility and control
- **Developers:** See and manage their own VMs only
- **Agents:** Use CLI/MCP, not the dashboard

**Authentication:** Tailscale identity. Dashboard listens on Tailscale interface; user identified via `whois` on connection. No separate auth system.

## Requirements

### Observability
- Logs: Stream stdout/stderr from VMs
- Status: VM state (running/stopped/failed), uptime, owner
- Resource metrics: CPU, memory, network usage per VM

### Fleet Management
- Group VMs by repository
- Filter by owner, status, resource usage
- Quick actions: stop, destroy, snapshot
- Alert indicators for problem VMs (high CPU, unresponsive)

### VM Debugging
- Live log streaming with pause/resume
- Historical log search
- Resource graphs over time
- Snapshot management (create, restore, delete)

### Access
- Copy SSH command for terminal access (v1)
- In-browser terminal via xterm.js (v2, designed for but not implemented initially)

### Responsive
- Full functionality on tablet/phone, reflowed for smaller screens

---

## Architecture

### Technology Stack

Single Go binary extending `stockyardd`:
- Static frontend assets (HTML/CSS/JS)
- REST API wrapping existing gRPC service
- WebSocket endpoint for real-time updates

**Frontend:** htmx + Alpine.js + Tailwind CSS
- No build step, no bundler
- Server-rendered HTML with dynamic updates via HTML partials
- Works with Go's `html/template`

**Why this approach:**
- Single binary deployment
- No Node.js in build chain
- Tailscale auth simpler in single process
- Focus on behavior over framework complexity

### API Layer

REST endpoints wrapping gRPC service:

```
GET  /api/vms                 # List VMs (filtered by user role)
GET  /api/vms/:id             # Get VM details
POST /api/vms/:id/stop        # Stop VM
POST /api/vms/:id/destroy     # Destroy VM
POST /api/vms/:id/snapshots   # Create snapshot
GET  /api/vms/:id/snapshots   # List snapshots
POST /api/vms/:id/snapshots/:name/restore  # Restore snapshot
GET  /api/vms/:id/logs        # WebSocket for log streaming
GET  /api/vms/:id/metrics     # WebSocket for metrics streaming
GET  /api/activity            # Activity feed (WebSocket)
GET  /api/user                # Current user info
```

### Real-time Updates

WebSocket connections for:
- Log streaming per VM
- Metrics streaming per VM (CPU, memory, network)
- Activity feed (fleet-wide events)
- VM status changes

---

## UI Design

### Navigation Structure

```
┌─────────────────────────────────────────────────────────┐
│ [≡] Stockyard              [Activity] [⚠ 2] [@jesse]   │
├────────┬────────────────────────────────────────────────┤
│   🏠   │                                                │
│  Fleet │  ┌─ Contextual Tabs ─────────────────────┐    │
│        │  │ Overview │ By Repo │ By Owner │ Alerts│    │
│   📊   │  └───────────────────────────────────────┘    │
│ Activity│                                               │
│        │  ┌─ Main Content ────────────────────────┐    │
│   ⚙️   │  │                                       │    │
│ Settings│  │                                       │    │
│        │  │                                       │    │
│        │  └───────────────────────────────────────┘    │
│ [◀]    │                                                │
└────────┴────────────────────────────────────────────────┘
```

**Sidebar:**
- Expanded: Icons + labels (~200px)
- Collapsed: Icons only (~60px), labels as tooltips
- Toggle at bottom, state in localStorage
- Auto-collapses on narrow viewports

**Top bar:**
- Left: Collapse toggle, wordmark
- Right: Activity feed button (slide-over), alert badge, user avatar

**Contextual tabs:**
- Below top bar, inside main content
- Change based on current section
- Selection persisted in URL

**Responsive breakpoints:**
- Desktop (>1024px): Full sidebar + split view
- Tablet (768-1024px): Collapsed sidebar, no split view
- Mobile (<768px): Bottom nav or hamburger, single column

### Fleet Overview (Split View)

```
┌─ Fleet ─────────────────────────────────────────────────┐
│ Overview │ By Repo │ By Owner │ Alerts                  │
├─────────────────────────────────┬───────────────────────┤
│                                 │                       │
│  ┌─ stockyard (3 VMs) ────────┐ │  vm-abc123            │
│  │ ● vm-abc123  jesse  45% 2GB│ │  ━━━━━━━━━━━━━━━━━━━  │
│  │ ● vm-def456  bot    12% 1GB│ │  Status: running      │
│  │ ○ vm-ghi789  jesse  --     │ │  Owner: jesse         │
│  └────────────────────────────┘ │  Repo: stockyard      │
│                                 │  Uptime: 2h 34m       │
│  ┌─ claude-code (5 VMs) ──────┐ │                       │
│  │ ● vm-jkl012  bot    23% 3GB│ │  CPU ▁▂▃▄▅▄▃▄▅ 45%   │
│  │ ● vm-mno345  bot    87% 4GB│ │  Mem ▃▃▃▄▄▄▄▄▄ 2.1GB │
│  │   ⚠ high CPU               │ │                       │
│  └────────────────────────────┘ │  [Copy SSH] [⋮]       │
│                                 │                       │
└─────────────────────────────────┴───────────────────────┘
```

**Left panel (grouped cards):**
- Cards grouped by repository, collapsible
- Header: repo name, VM count, aggregate status
- VM rows: status dot, ID, owner, CPU%, memory
- Problem VMs highlighted with warning icon
- Click to select (updates right panel)
- Double-click opens full detail

**Right panel (preview):**
- Selected VM's key info
- Sparklines for CPU/memory (last 15 min)
- Recent log lines (non-streaming)
- Quick actions: Copy SSH, dropdown menu
- "Open full view" link

### VM Detail View

```
┌─ vm-abc123 ─────────────────────────────────────────────┐
│ ← Back to Fleet                                         │
├─────────────────────────────────────────────────────────┤
│  ● running    stockyard    jesse    Started 2h ago      │
│                                    [Copy SSH] [⋮ Stop]  │
├─────────────────────────────────────────────────────────┤
│  ┌─ Resources ──────────────────────────────────────┐   │
│  │ CPU          Memory       Network      Disk      │   │
│  │ 45%          2.1GB/4GB    1.2MB/s      12GB used │   │
│  │ ▁▂▃▄▅▄▃▄▅   ▃▃▄▄▄▄▄▄▄   ▂▁▂▃▂▁▂▃    ▄▄▄▄▄▄▄▄▄  │   │
│  │ [▼ Expand to full charts]                        │   │
│  └──────────────────────────────────────────────────┘   │
│                                                         │
│  ┌─ Logs ───────────────────────────────────────────┐   │
│  │ [● Live] [History]              [Filter...] [↓]  │   │
│  │ 14:32:01  Cloning repository...                  │   │
│  │ 14:32:05  Repository cloned                      │   │
│  │ 14:32:06  Running npm install...                 │   │
│  │ █                                                │   │
│  └──────────────────────────────────────────────────┘   │
│                                                         │
│  ┌─ Details ────────────────────────────────────────┐   │
│  │ ID: vm-abc123                                    │   │
│  │ Repository: github.com/obra/stockyard            │   │
│  │ Git Ref: main (abc1234)                          │   │
│  │ Tailscale: vm-abc123.tail1234.ts.net             │   │
│  └──────────────────────────────────────────────────┘   │
│                                                         │
│  ┌─ Snapshots (3) ──────────────────────────────────┐   │
│  │ checkpoint-1   "before refactor"   2h ago  [⋮]   │   │
│  │ auto-save      --                  1h ago  [⋮]   │   │
│  │ [+ Create Snapshot]                              │   │
│  └──────────────────────────────────────────────────┘   │
└─────────────────────────────────────────────────────────┘
```

**Panels:**
- **Resources:** Sparklines default, expandable to full charts
- **Logs:** Live streaming via WebSocket, toggle to History for search
- **Details:** Static info with copy buttons
- **Snapshots:** List with create/restore/delete actions

### Alert Indicators

- Badge in top bar showing problem count
- Click filters fleet view to show only problem VMs
- Problem conditions: CPU >90% for >5min, memory >95%, unresponsive

### Activity Feed

- Slide-over panel from right
- Timeline of fleet events: VM started, stopped, failed, snapshot created
- Filterable by event type
- Links to relevant VM detail view

---

## Implementation Phases

### Phase 1: Foundation
- REST API layer wrapping gRPC
- Tailscale auth integration
- Basic HTML templates with htmx
- Fleet list view (no grouping yet)
- VM detail view (static, no streaming)
- Copy SSH button

### Phase 2: Real-time
- WebSocket infrastructure
- Log streaming
- Metrics streaming
- Live status updates

### Phase 3: Polish
- Grouped cards layout
- Split view
- Sparklines and charts
- Activity feed
- Alert indicators
- Responsive layouts

### Phase 4: Terminal (v2)
- xterm.js integration
- WebSocket SSH proxy
- Session management

---

## Open Questions

- Metrics storage: Keep in-memory ring buffer or persist to SQLite?
- Log retention: How much history to keep? Truncate on VM destroy?
- Rate limiting: Need to throttle WebSocket updates?

---

## Future Considerations (Not in Scope)

- Cost tracking
- Multi-host fleet management
- User management beyond Tailscale identity
- Audit logging
