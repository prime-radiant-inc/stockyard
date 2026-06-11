# Multi-Image Phase 1.5 (image CLI surface) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Land the complete `stockyard image ls|import|rm` surface (proto → daemon → backend → CLI) with macOS semantics, so phase 2 is backend-only work.

**Architecture:** Three new RPCs. `ListImages` flows daemon → backend via a new `vmbackend.ImageLister` optional interface (apple-container implements it by parsing `container image ls --format json`). `ImportImage`/`RemoveImage` are daemon-side per-backend guidance errors until phase 2: apple-container redirects to the `container` CLI; Firecracker cites phase 2. The CLI gets its first cobra command group.

**Tech Stack:** Go, protobuf/gRPC, cobra, the existing `fakeRunner` and `newTestGRPCServer` test seams.

**Spec:** `docs/superpowers/specs/2026-06-10-multi-image-design.md`, section "Phase 1.5 — image CLI surface" (PRI-2150). Branch: `matt/pri-2150-image-cli-surface`.

**Conventions:** Sign commits `Co-Authored-By: <YourName>@impl (<model>)`. `make proto` needs `PATH=/Users/mw/go/bin:$PATH` in non-interactive shells (protoc plugins live there). Non-goals: any registry behavior (phase 2), mutating the `container` store, dashboard exposure.

**Verified ground truth** (from a live `container image ls --format json`, 0.12.x):

```json
[{"fullSize":"4 MB","descriptor":{"size":9218,"mediaType":"...","digest":"sha256:48b0...","annotations":{"org.opencontainers.image.created":"2026-06-04T21:02:14Z"}},"reference":"docker.io/library/alpine:3.21"}]
```

`fullSize` is a human string; `annotations` is present only on some images; `reference` is fully qualified.

---

### Task 1: Proto — image RPCs and messages

**Files:**
- Modify: `api/stockyard.proto` (service block lines 7-20; append messages at end of file, currently 127 lines)
- Regenerate: `pkg/api/v1/stockyard.pb.go`, `pkg/api/v1/stockyard_grpc.pb.go` (service change — BOTH regenerate this time)

- [ ] **Step 1: Add RPCs** — in the `service Stockyard` block, after the `GetLogs` line add:

```proto

    rpc ListImages(ListImagesRequest) returns (ListImagesResponse);
    rpc ImportImage(ImportImageRequest) returns (ImportImageResponse);
    rpc RemoveImage(RemoveImageRequest) returns (RemoveImageResponse);
```

- [ ] **Step 2: Append messages** at end of file:

```proto

message ListImagesRequest {}

message ListImagesResponse {
    repeated ImageInfo images = 1;
}

message ImageInfo {
    string reference = 1;   // OCI ref, as the store reports it
    string digest = 2;      // e.g. "sha256:48b0..."
    string size = 3;        // Human-readable ("4 MB"); display-only
    string created_at = 4;  // Best-effort; empty if the image carries no created annotation
}

message ImportImageRequest {
    string name = 1;         // Registry name (OCI-style ref)
    string rootfs_path = 2;  // Path on the daemon host
    string kernel_path = 3;  // Optional; empty = shared default kernel
}

message ImportImageResponse {}

message RemoveImageRequest {
    string name = 1;
}

message RemoveImageResponse {}
```

- [ ] **Step 3: Regenerate and build**

Run: `cd /Users/mw/Code/prime/stockyard && PATH=/Users/mw/go/bin:$PATH make proto && CGO_ENABLED=0 go build ./...`
Expected: success. Both generated files diff (service changed). The daemon still compiles because `grpcServer` embeds `pb.UnimplementedStockyardServer`, which now provides default Unimplemented handlers for the three new RPCs.

- [ ] **Step 4: Commit**

```bash
git add api/stockyard.proto pkg/api/v1/stockyard.pb.go pkg/api/v1/stockyard_grpc.pb.go
git commit -m "feat(PRI-2150): ListImages/ImportImage/RemoveImage proto surface"
```

---

### Task 2: vmbackend — `ImageInfo` + `ImageLister`

**Files:**
- Modify: `pkg/vmbackend/backend.go` (append after the `ImageValidator` interface at end of file)

Declarations only; no test.

- [ ] **Step 1: Append to backend.go**:

```go
// ImageInfo describes one image in a backend's local store, for display.
// Size is a human-readable string (e.g. "4 MB") — backends format it;
// CreatedAt is best-effort and may be empty.
type ImageInfo struct {
	Reference string
	Digest    string
	Size      string
	CreatedAt string
}

// ImageLister is implemented by backends that can enumerate their local
// image store (apple-container today; the Firecracker registry in PRI-2150
// phase 2). Like ImageValidator above, it lives in this non-build-tagged
// file so daemon code can reference it on all platforms.
type ImageLister interface {
	// ListImages returns the images available on this host.
	ListImages(ctx context.Context) ([]ImageInfo, error)
}
```

- [ ] **Step 2: Build and commit**

Run: `CGO_ENABLED=0 go build ./pkg/vmbackend/`

```bash
git add pkg/vmbackend/backend.go
git commit -m "feat(PRI-2150): ImageInfo and ImageLister backend seam"
```

---

### Task 3: apple-container `ListImages`

**Files:**
- Test: `pkg/vmbackend/apple_container_test.go`
- Modify: `pkg/vmbackend/apple_container.go` (append at end of file, currently 408 lines)

- [ ] **Step 1: Write the failing tests** — add to apple_container_test.go (the fakeRunner already supports two-token keys like `"image ls"`):

```go
func TestAppleContainerBackend_ListImages(t *testing.T) {
	fr := newFakeRunner()
	fr.outputs["image ls"] = `[
	  {"fullSize":"4 MB","descriptor":{"size":9218,"mediaType":"application/vnd.oci.image.index.v1+json","digest":"sha256:48b0309c"},"reference":"docker.io/library/alpine:3.21"},
	  {"fullSize":"655.6 MB","descriptor":{"size":375,"digest":"sha256:ec1a1519","annotations":{"org.opencontainers.image.created":"2026-06-04T21:02:14Z"}},"reference":"docker.io/library/prudence-vm:dev"}
	]`
	b := newAppleContainerBackendWithRunner(AppleContainerConfig{Image: "stockyard-vm:latest"}, fr.run)

	images, err := b.ListImages(context.Background())
	if err != nil {
		t.Fatalf("ListImages: %v", err)
	}
	if len(images) != 2 {
		t.Fatalf("expected 2 images, got %d", len(images))
	}
	joined := strings.Join(fr.calls[0], " ")
	if !strings.Contains(joined, "image ls --format json") {
		t.Errorf("expected `image ls --format json` call; got: %s", joined)
	}
	want := ImageInfo{Reference: "docker.io/library/alpine:3.21", Digest: "sha256:48b0309c", Size: "4 MB", CreatedAt: ""}
	if images[0] != want {
		t.Errorf("images[0] = %+v, want %+v", images[0], want)
	}
	if images[1].CreatedAt != "2026-06-04T21:02:14Z" {
		t.Errorf("images[1].CreatedAt = %q, want created annotation", images[1].CreatedAt)
	}
}

func TestAppleContainerBackend_ListImages_Error(t *testing.T) {
	fr := newFakeRunner()
	fr.errs["image ls"] = fmt.Errorf("container daemon down")
	b := newAppleContainerBackendWithRunner(AppleContainerConfig{Image: "stockyard-vm:latest"}, fr.run)

	if _, err := b.ListImages(context.Background()); err == nil {
		t.Fatal("expected error when image ls fails")
	}
}
```

Also extend the interface-satisfaction test (`TestAppleContainerBackend_ImplementsInterface`) with:

```go
	var _ ImageLister = (*AppleContainerBackend)(nil)
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./pkg/vmbackend/ -run TestAppleContainerBackend_ListImages -v`
Expected: compile error — `b.ListImages undefined`.

- [ ] **Step 3: Implement** — append to apple_container.go:

```go
// imageListJSON is the subset of `container image ls --format json` we use
// (verified against the 0.12.x CLI). Parse defensively; annotations are
// present only on some images.
type imageListJSON struct {
	FullSize   string `json:"fullSize"`
	Descriptor struct {
		Digest      string            `json:"digest"`
		Annotations map[string]string `json:"annotations"`
	} `json:"descriptor"`
	Reference string `json:"reference"`
}

// ListImages enumerates the local `container` image store.
// Implements vmbackend.ImageLister.
func (b *AppleContainerBackend) ListImages(ctx context.Context) ([]ImageInfo, error) {
	out, err := b.run(ctx, b.cfg.ContainerBin, "image", "ls", "--format", "json")
	if err != nil {
		return nil, fmt.Errorf("container image ls: %w", err)
	}
	var arr []imageListJSON
	if err := json.Unmarshal(out, &arr); err != nil {
		return nil, fmt.Errorf("parse container image ls JSON: %w", err)
	}
	images := make([]ImageInfo, 0, len(arr))
	for _, img := range arr {
		images = append(images, ImageInfo{
			Reference: img.Reference,
			Digest:    img.Descriptor.Digest,
			Size:      img.FullSize,
			CreatedAt: img.Descriptor.Annotations["org.opencontainers.image.created"],
		})
	}
	return images, nil
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./pkg/vmbackend/ -v`
Expected: ALL PASS.

- [ ] **Step 5: Commit**

```bash
git add pkg/vmbackend/apple_container.go pkg/vmbackend/apple_container_test.go
git commit -m "feat(PRI-2150): apple-container ListImages via container image ls"
```

---

### Task 4: Daemon — ListImages/ImportImage/RemoveImage handlers

**Files:**
- Modify: `pkg/daemon/grpc.go`
- Test: `pkg/daemon/grpc_test.go` (fixture `newTestGRPCServer(t, withTaskManager bool)` exists; `NewTaskManager(d, backend)` accepts a backend — pass a fake)

- [ ] **Step 1: Write the failing tests** — add to grpc_test.go:

```go
// fakeListerBackend implements vmbackend.Backend trivially plus ImageLister.
type fakeListerBackend struct {
	vmbackend.Backend // nil embed: only ListImages is called in these tests
	images            []vmbackend.ImageInfo
}

func (f *fakeListerBackend) ListImages(ctx context.Context) ([]vmbackend.ImageInfo, error) {
	return f.images, nil
}

func TestGRPCServer_ListImages_ListerBackend(t *testing.T) {
	s := newTestGRPCServer(t, true)
	s.daemon.cfg.Backend = "apple-container"
	s.daemon.tasks = NewTaskManager(s.daemon, &fakeListerBackend{
		images: []vmbackend.ImageInfo{{Reference: "stockyard-vm:latest", Digest: "sha256:abc", Size: "5.6 GB"}},
	})

	resp, err := s.ListImages(context.Background(), &pb.ListImagesRequest{})
	if err != nil {
		t.Fatalf("ListImages: %v", err)
	}
	if len(resp.Images) != 1 || resp.Images[0].Reference != "stockyard-vm:latest" {
		t.Errorf("unexpected images: %+v", resp.Images)
	}
}

func TestGRPCServer_ListImages_UnsupportedBackend(t *testing.T) {
	s := newTestGRPCServer(t, true) // TaskManager with nil backend
	s.daemon.cfg.Backend = "firecracker"

	_, err := s.ListImages(context.Background(), &pb.ListImagesRequest{})
	if status.Code(err) != codes.Unimplemented {
		t.Fatalf("expected Unimplemented, got %v", err)
	}
	if !strings.Contains(err.Error(), "firecracker") {
		t.Errorf("error should name the backend: %v", err)
	}
}

func TestGRPCServer_ImportImage_AppleContainerRedirects(t *testing.T) {
	s := newTestGRPCServer(t, true)
	s.daemon.cfg.Backend = "apple-container"

	_, err := s.ImportImage(context.Background(), &pb.ImportImageRequest{Name: "x", RootfsPath: "/tmp/x"})
	if status.Code(err) != codes.Unimplemented {
		t.Fatalf("expected Unimplemented, got %v", err)
	}
	if !strings.Contains(err.Error(), "container image") {
		t.Errorf("apple-container should redirect to the container CLI: %v", err)
	}
}

func TestGRPCServer_RemoveImage_FirecrackerCitesPhase2(t *testing.T) {
	s := newTestGRPCServer(t, true)
	s.daemon.cfg.Backend = "firecracker"

	_, err := s.RemoveImage(context.Background(), &pb.RemoveImageRequest{Name: "x"})
	if status.Code(err) != codes.Unimplemented {
		t.Fatalf("expected Unimplemented, got %v", err)
	}
	if !strings.Contains(err.Error(), "PRI-2150 phase 2") {
		t.Errorf("firecracker should cite phase 2: %v", err)
	}
}
```

Add imports to grpc_test.go if missing: `strings` and `github.com/obra/stockyard/pkg/vmbackend` (`codes`/`status` are already imported — verify). Drive-by fix while in the file: the fixture comment at grpc_test.go:41 says `// nil config = no firecracker client` — the second NewTaskManager param is a `vmbackend.Backend`; correct it to `// nil backend = no VM backend`.

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./pkg/daemon/ -run 'TestGRPCServer_ListImages|TestGRPCServer_ImportImage|TestGRPCServer_RemoveImage' -v`
Expected: FAIL — the embedded `UnimplementedStockyardServer` returns a generic "method ListImages not implemented" without the backend name, so the message-content assertions fail (the codes.Unimplemented assertions alone would pass — the content assertions are what force real handlers).

- [ ] **Step 3: Implement** — add to grpc.go:

```go
// ListImages enumerates the backend's local image store.
func (s *grpcServer) ListImages(ctx context.Context, req *pb.ListImagesRequest) (*pb.ListImagesResponse, error) {
	if s.daemon.tasks == nil {
		return nil, status.Error(codes.Unavailable, "task manager not initialized")
	}
	lister, ok := s.daemon.tasks.backend.(vmbackend.ImageLister)
	if !ok {
		return nil, status.Errorf(codes.Unimplemented,
			"image listing is not supported by the %s backend (PRI-2150 phase 2)", s.backendName())
	}
	images, err := lister.ListImages(ctx)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "list images: %v", err)
	}
	pbImages := make([]*pb.ImageInfo, len(images))
	for i, img := range images {
		pbImages[i] = &pb.ImageInfo{
			Reference: img.Reference,
			Digest:    img.Digest,
			Size:      img.Size,
			CreatedAt: img.CreatedAt,
		}
	}
	return &pb.ListImagesResponse{Images: pbImages}, nil
}

// ImportImage and RemoveImage are daemon-side guidance until the Firecracker
// registry (PRI-2150 phase 2). The apple-container store is authoritative and
// stockyard does not mutate it — that redirect is permanent, not a stopgap.
// NOTE: the `container` CLI has no `image import`; its verbs are load/pull/rm,
// so the redirect names the real command, not the RPC verb.
func (s *grpcServer) ImportImage(ctx context.Context, req *pb.ImportImageRequest) (*pb.ImportImageResponse, error) {
	return nil, s.imageMutationUnsupported("import", "container image load` or `container image pull")
}

func (s *grpcServer) RemoveImage(ctx context.Context, req *pb.RemoveImageRequest) (*pb.RemoveImageResponse, error) {
	return nil, s.imageMutationUnsupported("remove", "container image rm")
}

func (s *grpcServer) imageMutationUnsupported(verb, containerCmd string) error {
	if s.backendName() == "apple-container" {
		return status.Errorf(codes.Unimplemented,
			"the apple-container image store is managed by the container CLI; use `%s` on the daemon host", containerCmd)
	}
	return status.Errorf(codes.Unimplemented,
		"image %s arrives with the %s image registry (PRI-2150 phase 2)", verb, s.backendName())
}

// backendName returns the configured backend, naming the default explicitly.
func (s *grpcServer) backendName() string {
	if s.daemon.cfg.Backend == "" {
		return "firecracker"
	}
	return s.daemon.cfg.Backend
}
```

Note: `vmbackend` must be added to grpc.go's imports (`github.com/obra/stockyard/pkg/vmbackend`).

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./pkg/daemon/ -v`
Expected: ALL PASS.

- [ ] **Step 5: Commit**

```bash
git add pkg/daemon/grpc.go pkg/daemon/grpc_test.go
git commit -m "feat(PRI-2150): daemon image RPC handlers with per-backend guidance"
```

---

### Task 5: Client wrapper + CLI command group

**Files:**
- Modify: `pkg/client/client.go` (wrapper methods — match the style of `ListTasks`/`ListSnapshots` there)
- Create: `cmd/stockyard/image.go`

No unit test (plumbing; covered by smoke). First cobra command *group* in the repo — `imageCmd` is the parent, subcommands attach to it.

- [ ] **Step 1: Client wrapper methods** — add to pkg/client/client.go after `ListTasks` (~line 105). The generated client field is `c.client` (verified):

```go
// ListImages returns the images available in the daemon's image store.
func (c *Client) ListImages(ctx context.Context) ([]*pb.ImageInfo, error) {
	resp, err := c.client.ListImages(ctx, &pb.ListImagesRequest{})
	if err != nil {
		return nil, err
	}
	return resp.Images, nil
}

// ImportImage registers a rootfs (and optional kernel) under name.
func (c *Client) ImportImage(ctx context.Context, name, rootfsPath, kernelPath string) error {
	_, err := c.client.ImportImage(ctx, &pb.ImportImageRequest{Name: name, RootfsPath: rootfsPath, KernelPath: kernelPath})
	return err
}

// RemoveImage removes a registered image by name.
func (c *Client) RemoveImage(ctx context.Context, name string) error {
	_, err := c.client.RemoveImage(ctx, &pb.RemoveImageRequest{Name: name})
	return err
}
```

- [ ] **Step 2: Create cmd/stockyard/image.go**:

```go
// cmd/stockyard/image.go
package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"
)

var imageCmd = &cobra.Command{
	Use:   "image",
	Short: "Manage the daemon's image store",
}

var imageLsCmd = &cobra.Command{
	Use:   "ls",
	Short: "List images available on the daemon host",
	RunE: func(cmd *cobra.Command, args []string) error {
		c, err := getClient()
		if err != nil {
			return err
		}
		defer c.Close()

		images, err := c.ListImages(context.Background())
		if err != nil {
			return err
		}
		if len(images) == 0 {
			fmt.Println("No images found")
			return nil
		}

		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(w, "REFERENCE\tDIGEST\tSIZE\tCREATED")
		for _, img := range images {
			created := img.CreatedAt
			if created == "" {
				created = "-"
			}
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\n",
				img.Reference, shortDigest(img.Digest), img.Size, created)
		}
		w.Flush()
		return nil
	},
}

var (
	imageImportRootfs string
	imageImportKernel string
)

var imageImportCmd = &cobra.Command{
	Use:   "import <name>",
	Short: "Register a rootfs image with the daemon (Firecracker registry)",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		c, err := getClient()
		if err != nil {
			return err
		}
		defer c.Close()
		return c.ImportImage(context.Background(), args[0], imageImportRootfs, imageImportKernel)
	},
}

var imageRmCmd = &cobra.Command{
	Use:   "rm <name>",
	Short: "Remove a registered image",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		c, err := getClient()
		if err != nil {
			return err
		}
		defer c.Close()
		return c.RemoveImage(context.Background(), args[0])
	},
}

// shortDigest trims "sha256:" and truncates for table display.
func shortDigest(d string) string {
	d = strings.TrimPrefix(d, "sha256:")
	if len(d) > 12 {
		return d[:12]
	}
	if d == "" {
		return "-"
	}
	return d
}

func init() {
	imageImportCmd.Flags().StringVar(&imageImportRootfs, "rootfs", "", "Path to the rootfs image on the daemon host (required)")
	imageImportCmd.Flags().StringVar(&imageImportKernel, "kernel", "", "Path to a per-image kernel on the daemon host (default: shared kernel)")
	imageImportCmd.MarkFlagRequired("rootfs")
	imageCmd.AddCommand(imageLsCmd, imageImportCmd, imageRmCmd)
	rootCmd.AddCommand(imageCmd)
}
```

- [ ] **Step 3: Build and verify**

Run: `CGO_ENABLED=0 go build -o bin/stockyard ./cmd/stockyard && ./bin/stockyard image --help`
Expected: shows ls / import / rm subcommands.

- [ ] **Step 4: Commit**

```bash
git add pkg/client/client.go cmd/stockyard/image.go
git commit -m "feat(PRI-2150): stockyard image ls/import/rm command group"
```

---

### Task 6: Full verification

- [ ] Run: `make test` — all green.
- [ ] Run: `CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build ./...` — clean.
- [ ] Run: `make build` — binaries in `bin/`.

---

### Task 7: macOS e2e smoke (required before push)

Same scratch-instance recipe as phase 1 (memory: `STOCKYARD_CONFIG_DIR`, never the default socket; daemon always tries 1Password first — harmless). Steps:

**Every `stockyard` command below MUST carry `STOCKYARD_CONFIG_DIR=/tmp/stockyard-smoke15`** — the CLI resolves the real default socket without it (this is the trap in the project memory).

- [ ] Scratch config at `/tmp/stockyard-smoke15/` (same JSON shape as phase 1; `apple_container.image` = an existing ref from `container image ls`; `http.enabled: false`; scratch socket/data dir). Start daemon with `STOCKYARD_CONFIG_DIR=/tmp/stockyard-smoke15`, log to file, record PID.
- [ ] `stockyard image ls` → table matches `container image ls` content (spot-check 2 refs, sizes present, digests truncated to 12 hex chars).
- [ ] `stockyard image import foo --rootfs /tmp/nope` → error mentioning `container image load` / `container image pull` (the apple-container guidance), non-zero exit.
- [ ] `stockyard image rm foo` → error redirecting to `container image rm`.
- [ ] Regression: `stockyard run --name smoke15 --no-tailscale --image <existing-ref>` → task runs; `stockyard list` shows the IMAGE column. Then `destroy --force` it.
- [ ] Cleanup: kill daemon by recorded PID, `rm -rf /tmp/stockyard-smoke15`, verify `container ls --all` shows nothing of ours.
- [ ] Record outputs for the PR description.
