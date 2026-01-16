# Stockyard Implementation - Part 3: Flintlock Integration (Phase 7)

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Integrate with Flintlock to create, manage, and destroy Firecracker micro-VMs.

**Reference:**
- Design doc at `docs/plans/2026-01-16-stockyard-design.md`
- Flintlock docs: https://flintlock.liquidmetal.dev/docs/intro/

---

## Phase 7: Flintlock Integration

### Task 7.1: Add Flintlock Dependencies

**Files:**
- Modify: `go.mod`

**Step 1: Add Flintlock client dependency**

```bash
go get github.com/liquidmetal-dev/flintlock/client
go get github.com/liquidmetal-dev/flintlock/api/services/microvm/v1alpha1
```

**Step 2: Verify**

```bash
go mod tidy
go build ./...
```

**Step 3: Commit**

```bash
git add go.mod go.sum
git commit -m "deps: add Flintlock client dependencies"
```

---

### Task 7.2: Create Flintlock Client Wrapper

**Files:**
- Create: `pkg/flintlock/client.go`
- Create: `pkg/flintlock/client_test.go`

**Step 1: Write test**

```go
// pkg/flintlock/client_test.go
package flintlock

import (
    "testing"
)

func TestVMConfig_Validate(t *testing.T) {
    tests := []struct {
        name    string
        config  VMConfig
        wantErr bool
    }{
        {
            name: "valid config",
            config: VMConfig{
                ID:       "task-123",
                VCPU:     2,
                MemoryMB: 4096,
                Image:    "ghcr.io/obra/stockyard-vm:latest",
            },
            wantErr: false,
        },
        {
            name: "missing ID",
            config: VMConfig{
                VCPU:     2,
                MemoryMB: 4096,
                Image:    "ghcr.io/obra/stockyard-vm:latest",
            },
            wantErr: true,
        },
        {
            name: "zero VCPU",
            config: VMConfig{
                ID:       "task-123",
                VCPU:     0,
                MemoryMB: 4096,
                Image:    "ghcr.io/obra/stockyard-vm:latest",
            },
            wantErr: true,
        },
    }

    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            err := tt.config.Validate()
            if (err != nil) != tt.wantErr {
                t.Errorf("Validate() error = %v, wantErr %v", err, tt.wantErr)
            }
        })
    }
}

func TestGenerateVMID(t *testing.T) {
    id1 := GenerateVMID()
    id2 := GenerateVMID()

    if id1 == id2 {
        t.Error("generated IDs should be unique")
    }

    if len(id1) < 8 {
        t.Error("ID should be at least 8 characters")
    }
}
```

**Step 2: Run test**

```bash
go test ./pkg/flintlock/... -v
```

Expected: FAIL

**Step 3: Implement Flintlock client**

```go
// pkg/flintlock/client.go
package flintlock

import (
    "context"
    "crypto/rand"
    "encoding/hex"
    "fmt"

    "google.golang.org/grpc"
    "google.golang.org/grpc/credentials/insecure"

    flintlockv1 "github.com/liquidmetal-dev/flintlock/api/services/microvm/v1alpha1"
    flintlocktypes "github.com/liquidmetal-dev/flintlock/api/types"
)

// Client wraps the Flintlock gRPC client
type Client struct {
    conn   *grpc.ClientConn
    client flintlockv1.MicroVMClient
}

// VMConfig represents configuration for creating a VM
type VMConfig struct {
    ID              string
    Namespace       string
    VCPU            int32
    MemoryMB        int32
    Image           string
    KernelImage     string
    WorkspacePath   string   // Host path to mount as /workspace
    CloudInitData   string   // Base64-encoded cloud-init user-data
    NetworkConfig   NetworkConfig
    Metadata        map[string]string
}

// NetworkConfig represents VM network configuration
type NetworkConfig struct {
    EnableTailscale bool
    StaticIP        string // Optional: static IP for TAP device
    GatewayIP       string
}

// VM represents a running VM
type VM struct {
    ID        string
    Namespace string
    Status    string
    IP        string
}

// Validate checks if the config is valid
func (c *VMConfig) Validate() error {
    if c.ID == "" {
        return fmt.Errorf("ID is required")
    }
    if c.VCPU <= 0 {
        return fmt.Errorf("VCPU must be positive")
    }
    if c.MemoryMB <= 0 {
        return fmt.Errorf("MemoryMB must be positive")
    }
    if c.Image == "" {
        return fmt.Errorf("Image is required")
    }
    return nil
}

// GenerateVMID generates a unique VM ID
func GenerateVMID() string {
    b := make([]byte, 8)
    rand.Read(b)
    return fmt.Sprintf("vm-%s", hex.EncodeToString(b))
}

// NewClient creates a new Flintlock client
func NewClient(endpoint string) (*Client, error) {
    conn, err := grpc.Dial(
        endpoint,
        grpc.WithTransportCredentials(insecure.NewCredentials()),
    )
    if err != nil {
        return nil, fmt.Errorf("failed to connect to Flintlock: %w", err)
    }

    return &Client{
        conn:   conn,
        client: flintlockv1.NewMicroVMClient(conn),
    }, nil
}

// Close closes the client connection
func (c *Client) Close() error {
    return c.conn.Close()
}

// CreateVM creates a new micro-VM
func (c *Client) CreateVM(ctx context.Context, cfg *VMConfig) (*VM, error) {
    if err := cfg.Validate(); err != nil {
        return nil, err
    }

    namespace := cfg.Namespace
    if namespace == "" {
        namespace = "stockyard"
    }

    // Build the MicroVM spec
    spec := &flintlocktypes.MicroVMSpec{
        Id:        cfg.ID,
        Namespace: namespace,
        Vcpu:      cfg.VCPU,
        MemoryInMb: cfg.MemoryMB,
        Kernel: &flintlocktypes.Kernel{
            Image: cfg.KernelImage,
        },
        RootVolume: &flintlocktypes.Volume{
            Id:         "root",
            IsReadOnly: false,
            Source: &flintlocktypes.VolumeSource{
                ContainerSource: &flintlocktypes.ContainerVolumeSource{
                    Image: cfg.Image,
                },
            },
        },
        Metadata: cfg.Metadata,
    }

    // Add workspace volume mount if specified
    if cfg.WorkspacePath != "" {
        spec.AdditionalVolumes = append(spec.AdditionalVolumes, &flintlocktypes.Volume{
            Id:         "workspace",
            IsReadOnly: false,
            MountPoint: "/workspace",
            Source: &flintlocktypes.VolumeSource{
                // Note: Flintlock uses different mount types
                // This may need adjustment based on Flintlock version
            },
        })
    }

    // Add network interface
    spec.Interfaces = append(spec.Interfaces, &flintlocktypes.NetworkInterface{
        DeviceId: "eth0",
        Type:     flintlocktypes.NetworkInterface_MACVTAP,
    })

    // Create the VM
    req := &flintlockv1.CreateMicroVMRequest{
        Microvm: spec,
    }

    resp, err := c.client.CreateMicroVM(ctx, req)
    if err != nil {
        return nil, fmt.Errorf("failed to create VM: %w", err)
    }

    return &VM{
        ID:        resp.Microvm.Spec.Id,
        Namespace: resp.Microvm.Spec.Namespace,
        Status:    resp.Microvm.Status.State.String(),
    }, nil
}

// GetVM gets VM information
func (c *Client) GetVM(ctx context.Context, namespace, id string) (*VM, error) {
    req := &flintlockv1.GetMicroVMRequest{
        Namespace: namespace,
        Id:        id,
    }

    resp, err := c.client.GetMicroVM(ctx, req)
    if err != nil {
        return nil, err
    }

    vm := &VM{
        ID:        resp.Microvm.Spec.Id,
        Namespace: resp.Microvm.Spec.Namespace,
        Status:    resp.Microvm.Status.State.String(),
    }

    // Extract IP if available
    for _, iface := range resp.Microvm.Status.NetworkInterfaces {
        if iface.HostDeviceName != "" {
            // IP might be available through other means
        }
    }

    return vm, nil
}

// DeleteVM deletes a VM
func (c *Client) DeleteVM(ctx context.Context, namespace, id string) error {
    req := &flintlockv1.DeleteMicroVMRequest{
        Namespace: namespace,
        Id:        id,
    }

    _, err := c.client.DeleteMicroVM(ctx, req)
    return err
}

// ListVMs lists all VMs in a namespace
func (c *Client) ListVMs(ctx context.Context, namespace string) ([]*VM, error) {
    req := &flintlockv1.ListMicroVMsRequest{
        Namespace: namespace,
    }

    resp, err := c.client.ListMicroVMs(ctx, req)
    if err != nil {
        return nil, err
    }

    vms := make([]*VM, len(resp.Microvm))
    for i, mvm := range resp.Microvm {
        vms[i] = &VM{
            ID:        mvm.Spec.Id,
            Namespace: mvm.Spec.Namespace,
            Status:    mvm.Status.State.String(),
        }
    }

    return vms, nil
}
```

**Step 4: Run tests**

```bash
go test ./pkg/flintlock/... -v
```

Expected: PASS

**Step 5: Commit**

```bash
git add pkg/flintlock/
git commit -m "feat: add Flintlock client wrapper"
```

---

### Task 7.3: Create Cloud-Init Generator

**Files:**
- Create: `pkg/flintlock/cloudinit.go`
- Create: `pkg/flintlock/cloudinit_test.go`

**Step 1: Write test**

```go
// pkg/flintlock/cloudinit_test.go
package flintlock

import (
    "encoding/base64"
    "strings"
    "testing"

    "gopkg.in/yaml.v3"
)

func TestCloudInitConfig_Generate(t *testing.T) {
    cfg := &CloudInitConfig{
        Hostname: "stockyard-task-123",
        Environment: map[string]string{
            "ANTHROPIC_API_KEY": "sk-test-123",
            "GITHUB_TOKEN":      "ghp_test456",
        },
        SSHAuthorizedKeys: []string{
            "ssh-ed25519 AAAA... user@host",
        },
        TailscaleAuthKey: "tskey-auth-xxx",
        WorkspacePath:    "/workspace",
    }

    userData, err := cfg.Generate()
    if err != nil {
        t.Fatalf("failed to generate cloud-init: %v", err)
    }

    // Decode base64
    decoded, err := base64.StdEncoding.DecodeString(userData)
    if err != nil {
        t.Fatalf("failed to decode base64: %v", err)
    }

    content := string(decoded)

    // Should start with cloud-init header
    if !strings.HasPrefix(content, "#cloud-config\n") {
        t.Error("should start with #cloud-config")
    }

    // Should contain hostname
    if !strings.Contains(content, "stockyard-task-123") {
        t.Error("should contain hostname")
    }

    // Should contain tailscale setup
    if !strings.Contains(content, "tailscale") {
        t.Error("should contain tailscale setup")
    }
}
```

**Step 2: Run test**

```bash
go test ./pkg/flintlock/... -v -run TestCloudInit
```

Expected: FAIL

**Step 3: Add yaml dependency and implement**

```bash
go get gopkg.in/yaml.v3
```

```go
// pkg/flintlock/cloudinit.go
package flintlock

import (
    "bytes"
    "encoding/base64"
    "fmt"
    "text/template"

    "gopkg.in/yaml.v3"
)

// CloudInitConfig holds configuration for cloud-init generation
type CloudInitConfig struct {
    Hostname          string
    Environment       map[string]string
    SSHAuthorizedKeys []string
    TailscaleAuthKey  string
    WorkspacePath     string
    PostCreateScript  string
}

// cloudInitData represents the cloud-init YAML structure
type cloudInitData struct {
    Hostname        string   `yaml:"hostname,omitempty"`
    ManageEtcHosts  bool     `yaml:"manage_etc_hosts,omitempty"`
    SSHAuthorizedKeys []string `yaml:"ssh_authorized_keys,omitempty"`
    WriteFiles      []writeFile `yaml:"write_files,omitempty"`
    Runcmd          []string `yaml:"runcmd,omitempty"`
}

type writeFile struct {
    Path        string `yaml:"path"`
    Content     string `yaml:"content"`
    Permissions string `yaml:"permissions,omitempty"`
}

// Generate generates base64-encoded cloud-init user-data
func (c *CloudInitConfig) Generate() (string, error) {
    data := cloudInitData{
        Hostname:       c.Hostname,
        ManageEtcHosts: true,
        SSHAuthorizedKeys: c.SSHAuthorizedKeys,
    }

    // Write environment file
    if len(c.Environment) > 0 {
        var envContent bytes.Buffer
        for k, v := range c.Environment {
            fmt.Fprintf(&envContent, "export %s=%q\n", k, v)
        }
        data.WriteFiles = append(data.WriteFiles, writeFile{
            Path:        "/etc/stockyard/env",
            Content:     envContent.String(),
            Permissions: "0600",
        })

        // Also write to profile.d for shell access
        data.WriteFiles = append(data.WriteFiles, writeFile{
            Path:        "/etc/profile.d/stockyard.sh",
            Content:     envContent.String(),
            Permissions: "0644",
        })
    }

    // Write Claude Code hooks for auto-snapshots
    hookContent := `{
  "hooks": {
    "PostToolUse": [{
      "command": "stockyard-snapshot \"$CLAUDE_TOOL_NAME\""
    }]
  }
}`
    data.WriteFiles = append(data.WriteFiles, writeFile{
        Path:        "/etc/stockyard/claude-hooks.json",
        Content:     hookContent,
        Permissions: "0644",
    })

    // Setup commands
    data.Runcmd = []string{
        "mkdir -p /etc/stockyard",
        "source /etc/stockyard/env 2>/dev/null || true",
    }

    // Tailscale setup
    if c.TailscaleAuthKey != "" {
        data.Runcmd = append(data.Runcmd,
            fmt.Sprintf("tailscale up --authkey=%s --hostname=%s --accept-routes",
                c.TailscaleAuthKey, c.Hostname),
        )
    }

    // Copy Claude hooks to user home
    data.Runcmd = append(data.Runcmd,
        "mkdir -p /home/vscode/.claude",
        "cp /etc/stockyard/claude-hooks.json /home/vscode/.claude/hooks.json",
        "chown -R vscode:vscode /home/vscode/.claude",
    )

    // Post-create script
    if c.PostCreateScript != "" {
        data.Runcmd = append(data.Runcmd, c.PostCreateScript)
    }

    // Generate YAML
    yamlData, err := yaml.Marshal(data)
    if err != nil {
        return "", fmt.Errorf("failed to marshal cloud-init: %w", err)
    }

    // Prepend cloud-config header
    fullContent := "#cloud-config\n" + string(yamlData)

    return base64.StdEncoding.EncodeToString([]byte(fullContent)), nil
}

// EnvironmentScript generates a shell script that exports environment variables
func (c *CloudInitConfig) EnvironmentScript() string {
    var buf bytes.Buffer
    buf.WriteString("#!/bin/bash\n")
    for k, v := range c.Environment {
        fmt.Fprintf(&buf, "export %s=%q\n", k, v)
    }
    return buf.String()
}
```

**Step 4: Run tests**

```bash
go test ./pkg/flintlock/... -v
```

Expected: PASS

**Step 5: Commit**

```bash
go mod tidy
git add pkg/flintlock/cloudinit.go pkg/flintlock/cloudinit_test.go go.mod go.sum
git commit -m "feat: add cloud-init generator for VMs"
```

---

### Task 7.4: Integrate Flintlock into Daemon

**Files:**
- Modify: `pkg/daemon/daemon.go`
- Create: `pkg/daemon/tasks.go`

**Step 1: Create task manager**

```go
// pkg/daemon/tasks.go
package daemon

import (
    "context"
    "fmt"
    "strings"
    "time"

    "github.com/obra/stockyard/pkg/flintlock"
)

// TaskManager handles task lifecycle
type TaskManager struct {
    daemon *Daemon
    flint  *flintlock.Client
}

// NewTaskManager creates a new task manager
func NewTaskManager(d *Daemon, flintEndpoint string) (*TaskManager, error) {
    flint, err := flintlock.NewClient(flintEndpoint)
    if err != nil {
        return nil, fmt.Errorf("failed to connect to Flintlock: %w", err)
    }

    return &TaskManager{
        daemon: d,
        flint:  flint,
    }, nil
}

// CreateTaskRequest contains the information needed to create a task
type CreateTaskRequest struct {
    Repo        string
    Ref         string
    Name        string
    Command     []string
    Env         map[string]string
    CPUs        int32
    MemoryMB    int32
    NoTailscale bool
}

// CreateTask creates a new task with a VM
func (tm *TaskManager) CreateTask(ctx context.Context, req *CreateTaskRequest) (*Task, error) {
    // Generate IDs
    taskID := flintlock.GenerateVMID()
    if req.Name != "" {
        // Use name as part of ID if provided
        safeName := strings.Map(func(r rune) rune {
            if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' {
                return r
            }
            return '-'
        }, strings.ToLower(req.Name))
        taskID = fmt.Sprintf("task-%s-%s", safeName, taskID[3:11])
    }

    // Create ZFS dataset for workspace
    if err := tm.daemon.zfs.CreateDataset(ctx, taskID); err != nil {
        return nil, fmt.Errorf("failed to create workspace: %w", err)
    }

    // Get workspace mountpoint
    workspacePath, err := tm.daemon.zfs.GetMountpoint(ctx, taskID)
    if err != nil {
        tm.daemon.zfs.DestroyDataset(ctx, taskID)
        return nil, fmt.Errorf("failed to get workspace path: %w", err)
    }

    // Fetch secrets
    env := make(map[string]string)
    for k, v := range req.Env {
        env[k] = v
    }

    // Add API keys from secrets provider
    secrets := []string{"anthropic-api-key", "github-token", "openai-api-key"}
    for _, name := range secrets {
        if val, err := tm.daemon.secrets.GetSecret(ctx, name); err == nil && val != "" {
            envKey := strings.ToUpper(strings.ReplaceAll(name, "-", "_"))
            env[envKey] = val
        }
    }

    // Get Tailscale auth key if enabled
    var tailscaleKey string
    if !req.NoTailscale {
        tailscaleKey, _ = tm.daemon.secrets.GetSecret(ctx, "tailscale-auth-key")
    }

    // Generate cloud-init
    cloudInit := &flintlock.CloudInitConfig{
        Hostname:         fmt.Sprintf("stockyard-%s", taskID),
        Environment:      env,
        TailscaleAuthKey: tailscaleKey,
        WorkspacePath:    workspacePath,
    }

    cloudInitData, err := cloudInit.Generate()
    if err != nil {
        tm.daemon.zfs.DestroyDataset(ctx, taskID)
        return nil, fmt.Errorf("failed to generate cloud-init: %w", err)
    }

    // Set defaults
    cpus := req.CPUs
    if cpus == 0 {
        cpus = 2
    }
    memory := req.MemoryMB
    if memory == 0 {
        memory = 4096
    }

    // Create VM
    vmConfig := &flintlock.VMConfig{
        ID:            taskID,
        Namespace:     "stockyard",
        VCPU:          cpus,
        MemoryMB:      memory,
        Image:         "ghcr.io/obra/stockyard-vm:latest",
        KernelImage:   "ghcr.io/obra/stockyard-kernel:latest",
        WorkspacePath: workspacePath,
        CloudInitData: cloudInitData,
        Metadata: map[string]string{
            "repo":    req.Repo,
            "ref":     req.Ref,
            "command": strings.Join(req.Command, " "),
        },
    }

    vm, err := tm.flint.CreateVM(ctx, vmConfig)
    if err != nil {
        tm.daemon.zfs.DestroyDataset(ctx, taskID)
        return nil, fmt.Errorf("failed to create VM: %w", err)
    }

    // Record task in database
    task := &Task{
        ID:        taskID,
        Name:      req.Name,
        Repo:      req.Repo,
        Ref:       req.Ref,
        Command:   strings.Join(req.Command, " "),
        Status:    "running",
        VMID:      vm.ID,
        CreatedAt: time.Now(),
    }

    if err := tm.daemon.state.CreateTask(task); err != nil {
        // Best effort cleanup
        tm.flint.DeleteVM(ctx, "stockyard", taskID)
        tm.daemon.zfs.DestroyDataset(ctx, taskID)
        return nil, fmt.Errorf("failed to record task: %w", err)
    }

    return task, nil
}

// StopTask stops a running task's VM
func (tm *TaskManager) StopTask(ctx context.Context, taskID string) error {
    task, err := tm.daemon.state.GetTask(taskID)
    if err != nil {
        return err
    }
    if task == nil {
        return fmt.Errorf("task not found")
    }

    // Delete VM (Flintlock doesn't have stop, only delete)
    if err := tm.flint.DeleteVM(ctx, "stockyard", taskID); err != nil {
        // Log but continue - VM might already be gone
        fmt.Printf("Warning: failed to delete VM: %v\n", err)
    }

    return tm.daemon.state.UpdateTaskStatus(taskID, "stopped")
}

// DestroyTask stops and removes a task completely
func (tm *TaskManager) DestroyTask(ctx context.Context, taskID string) error {
    // Stop first
    tm.StopTask(ctx, taskID)

    // Destroy ZFS dataset
    if err := tm.daemon.zfs.DestroyDataset(ctx, taskID); err != nil {
        fmt.Printf("Warning: failed to destroy workspace: %v\n", err)
    }

    return tm.daemon.state.DeleteTask(taskID)
}

// Close closes the task manager
func (tm *TaskManager) Close() error {
    return tm.flint.Close()
}
```

**Step 2: Update daemon to use TaskManager**

Add to `pkg/daemon/daemon.go`:

```go
// Add field to Daemon struct:
tasks *TaskManager

// In New(), after creating state:
// Note: TaskManager creation is deferred until Start() since we need config

// Add method:
func (d *Daemon) Tasks() *TaskManager {
    return d.tasks
}
```

**Step 3: Update gRPC server to use TaskManager**

Update `pkg/daemon/grpc.go` CreateTask method:

```go
func (s *grpcServer) CreateTask(ctx context.Context, req *pb.CreateTaskRequest) (*pb.CreateTaskResponse, error) {
    if s.daemon.tasks == nil {
        return nil, status.Error(codes.Unavailable, "task manager not initialized")
    }

    task, err := s.daemon.tasks.CreateTask(ctx, &CreateTaskRequest{
        Repo:        req.Repo,
        Ref:         req.Ref,
        Name:        req.Name,
        Command:     req.Command,
        Env:         req.Env,
        CPUs:        req.Cpus,
        MemoryMB:    parseMemory(req.Memory),
        NoTailscale: req.NoTailscale,
    })
    if err != nil {
        return nil, status.Errorf(codes.Internal, "failed to create task: %v", err)
    }

    hostname := ""
    if !req.NoTailscale {
        hostname = fmt.Sprintf("stockyard-%s", task.ID)
    }

    return &pb.CreateTaskResponse{
        TaskId:            task.ID,
        TailscaleHostname: hostname,
    }, nil
}

func parseMemory(s string) int32 {
    if s == "" {
        return 4096
    }
    // Parse "4G" or "4096" etc.
    s = strings.ToUpper(strings.TrimSpace(s))
    if strings.HasSuffix(s, "G") {
        var n int32
        fmt.Sscanf(s, "%dG", &n)
        return n * 1024
    }
    var n int32
    fmt.Sscanf(s, "%d", &n)
    return n
}
```

**Step 4: Verify compilation**

```bash
go build ./pkg/daemon/...
```

**Step 5: Commit**

```bash
git add pkg/daemon/tasks.go pkg/daemon/daemon.go pkg/daemon/grpc.go
git commit -m "feat: integrate Flintlock for VM management"
```

---

### Task 7.5: Configure Networking

**Files:**
- Create: `pkg/flintlock/network.go`

**Step 1: Implement network configuration**

```go
// pkg/flintlock/network.go
package flintlock

import (
    "fmt"
    "net"
    "os/exec"
)

// NetworkManager handles TAP device and network configuration
type NetworkManager struct {
    bridgeName string
    subnet     *net.IPNet
    nextIP     net.IP
}

// NewNetworkManager creates a network manager
func NewNetworkManager(bridgeName string, subnet string) (*NetworkManager, error) {
    _, ipnet, err := net.ParseCIDR(subnet)
    if err != nil {
        return nil, fmt.Errorf("invalid subnet: %w", err)
    }

    // Start from .2 (assuming .1 is the gateway)
    nextIP := make(net.IP, len(ipnet.IP))
    copy(nextIP, ipnet.IP)
    nextIP[3] = 2

    return &NetworkManager{
        bridgeName: bridgeName,
        subnet:     ipnet,
        nextIP:     nextIP,
    }, nil
}

// AllocateIP allocates the next available IP
func (nm *NetworkManager) AllocateIP() (string, error) {
    ip := nm.nextIP.String()

    // Increment for next allocation
    nm.nextIP[3]++
    if nm.nextIP[3] == 0 {
        nm.nextIP[2]++
    }

    return ip, nil
}

// CreateTAPDevice creates a TAP device for a VM
func (nm *NetworkManager) CreateTAPDevice(name string) error {
    cmd := exec.Command("ip", "tuntap", "add", "dev", name, "mode", "tap")
    if err := cmd.Run(); err != nil {
        return fmt.Errorf("failed to create TAP device: %w", err)
    }

    cmd = exec.Command("ip", "link", "set", "dev", name, "up")
    if err := cmd.Run(); err != nil {
        return fmt.Errorf("failed to bring up TAP device: %w", err)
    }

    // Add to bridge
    cmd = exec.Command("ip", "link", "set", "dev", name, "master", nm.bridgeName)
    if err := cmd.Run(); err != nil {
        return fmt.Errorf("failed to add TAP to bridge: %w", err)
    }

    return nil
}

// DeleteTAPDevice removes a TAP device
func (nm *NetworkManager) DeleteTAPDevice(name string) error {
    cmd := exec.Command("ip", "link", "delete", name)
    return cmd.Run()
}

// SetupBridge creates the bridge if it doesn't exist
func (nm *NetworkManager) SetupBridge() error {
    // Check if bridge exists
    cmd := exec.Command("ip", "link", "show", nm.bridgeName)
    if cmd.Run() == nil {
        return nil // Bridge exists
    }

    // Create bridge
    cmd = exec.Command("ip", "link", "add", "name", nm.bridgeName, "type", "bridge")
    if err := cmd.Run(); err != nil {
        return fmt.Errorf("failed to create bridge: %w", err)
    }

    // Assign IP to bridge (gateway)
    gatewayIP := nm.subnet.IP.String()
    gatewayIP = gatewayIP[:len(gatewayIP)-1] + "1" // .1 for gateway
    maskSize, _ := nm.subnet.Mask.Size()
    cmd = exec.Command("ip", "addr", "add", fmt.Sprintf("%s/%d", gatewayIP, maskSize), "dev", nm.bridgeName)
    if err := cmd.Run(); err != nil {
        return fmt.Errorf("failed to assign IP to bridge: %w", err)
    }

    // Bring up bridge
    cmd = exec.Command("ip", "link", "set", "dev", nm.bridgeName, "up")
    if err := cmd.Run(); err != nil {
        return fmt.Errorf("failed to bring up bridge: %w", err)
    }

    // Enable IP forwarding
    cmd = exec.Command("sysctl", "-w", "net.ipv4.ip_forward=1")
    cmd.Run()

    // Setup NAT
    cmd = exec.Command("iptables", "-t", "nat", "-A", "POSTROUTING", "-s", nm.subnet.String(), "-j", "MASQUERADE")
    cmd.Run()

    return nil
}
```

**Step 2: Commit**

```bash
git add pkg/flintlock/network.go
git commit -m "feat: add network configuration for VMs"
```

---

**End of Part 3. Continue with Part 4: VM Image.**
