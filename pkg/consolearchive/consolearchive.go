// Package consolearchive preserves VM console logs (stdout.log, stderr.log)
// in a bounded host-local archive before a VM's state directory is removed.
// Archiving is best-effort: the archiver logs every outcome and callers
// continue the destroy regardless of the returned error.
package consolearchive

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

const (
	stagingPrefix   = ".staging-"
	stagingMaxAge   = time.Hour
	entryTimeFormat = "20060102T150405Z"

	// headBytes is kept from the start of an oversized console; boot
	// evidence lives at the head and death evidence at the tail.
	headBytes = int64(8 << 20)
)

// consoleFiles are the files preserved from a VM state directory.
var consoleFiles = []string{"stdout.log", "stderr.log"}

// idPattern accepts daemon-generated VM ids and rejects anything that could
// influence the archive path. "." and ".." are rejected separately.
var idPattern = regexp.MustCompile(`^[a-zA-Z0-9._-]{1,64}$`)

// entryNamePattern matches only names produced by ArchiveVMDir. The final
// numeric component is the random suffix added by os.MkdirTemp.
var entryNamePattern = regexp.MustCompile(`^(\d{8}T\d{6}Z)-([a-zA-Z0-9._-]{1,64})-([0-9]+)$`)

// Archiver copies console logs from VM state directories into Dir, bounded by
// MaxTotalBytes, MaxAge, and MaxEntryBytes. A nil *Archiver disables
// archiving; callers treat nil as "skip".
type Archiver struct {
	Dir           string
	MaxTotalBytes int64
	MaxAge        time.Duration
	MaxEntryBytes int64
	// Logf receives outcome tokens; nil means log.Printf.
	Logf func(format string, args ...any)

	mu sync.Mutex
}

func (a *Archiver) logf(format string, args ...any) {
	if a.Logf != nil {
		a.Logf(format, args...)
		return
	}
	log.Printf(format, args...)
}

func validID(id string) bool {
	if id == "." || id == ".." {
		return false
	}
	return idPattern.MatchString(id)
}

func validArchiveEntryName(name string) bool {
	matches := entryNamePattern.FindStringSubmatch(name)
	if matches == nil || !validID(matches[2]) {
		return false
	}
	_, err := time.Parse(entryTimeFormat, matches[1])
	return err == nil
}

type fileMeta struct {
	Name          string `json:"name"`
	Bytes         int64  `json:"bytes"`
	OriginalBytes int64  `json:"original_bytes"`
	Truncated     bool   `json:"truncated"`
}

type entryMeta struct {
	TaskID      string     `json:"task_id"`
	Namespace   string     `json:"namespace"`
	Source      string     `json:"source"`
	DestroyedAt time.Time  `json:"destroyed_at"`
	Files       []fileMeta `json:"files"`
}

// ArchiveVMDir preserves vmDir's console logs as one archive entry named
// <utc-timestamp>-<id>-<random-suffix>. An in-progress entry lives under a
// ".staging-" prefix and is renamed into place only when complete, so a
// partial archive is always distinguishable. Every outcome is logged; the
// returned error is informational and must never block a destroy.
func (a *Archiver) ArchiveVMDir(vmDir, id, source string) (err error) {
	defer func() {
		if r := recover(); r != nil {
			a.logf("console_archive_panic task=%s source=%s err=%v", id, source, r)
			err = fmt.Errorf("console archive panicked: %v", r)
		}
	}()
	a.mu.Lock()
	defer a.mu.Unlock()

	if !validID(id) {
		err := fmt.Errorf("invalid vm id")
		a.logf("console_archive_failed task=%q source=%s err=%v", id, source, err)
		return err
	}

	var present []string
	for _, name := range consoleFiles {
		if info, err := os.Stat(filepath.Join(vmDir, name)); err == nil && info.Mode().IsRegular() {
			present = append(present, name)
		}
	}
	if len(present) == 0 {
		a.logf("console_archive_empty task=%s source=%s", id, source)
		return nil
	}

	now := time.Now().UTC()
	entryPrefix := now.Format(entryTimeFormat) + "-" + id + "-"
	if err := os.MkdirAll(a.Dir, 0o700); err != nil {
		a.logf("console_archive_failed task=%q source=%s err=%v", id, source, err)
		return err
	}
	staging, err := os.MkdirTemp(a.Dir, stagingPrefix+entryPrefix)
	if err != nil {
		a.logf("console_archive_failed task=%q source=%s err=%v", id, source, err)
		return err
	}
	entryName := strings.TrimPrefix(filepath.Base(staging), stagingPrefix)

	files, err := a.stage(vmDir, id, source, now, present, staging, entryName)
	if err != nil {
		_ = os.RemoveAll(staging)
		a.logf("console_archive_failed task=%q source=%s err=%v", id, source, err)
		return err
	}

	var total int64
	truncated := false
	for _, f := range files {
		total += f.Bytes
		truncated = truncated || f.Truncated
	}
	a.logf("console_archive_ok task=%s source=%s bytes=%d truncated=%t", id, source, total, truncated)
	_ = a.pruneLocked() // retention outcome is logged by the pruner
	return nil
}

// stage assembles the entry under the staging directory and renames it into
// place as the final, atomic step.
func (a *Archiver) stage(vmDir, id, source string, now time.Time, present []string, staging, entryName string) ([]fileMeta, error) {
	var files []fileMeta
	for _, name := range present {
		fm, err := a.archiveFile(filepath.Join(vmDir, name), filepath.Join(staging, name))
		if err != nil {
			return nil, err
		}
		files = append(files, fm)
	}
	meta := entryMeta{
		TaskID:      id,
		Namespace:   filepath.Base(filepath.Dir(vmDir)),
		Source:      source,
		DestroyedAt: now,
		Files:       files,
	}
	data, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile(filepath.Join(staging, "meta.json"), data, 0o600); err != nil {
		return nil, err
	}
	if err := os.Rename(staging, filepath.Join(a.Dir, entryName)); err != nil {
		return nil, err
	}
	return files, nil
}

func (a *Archiver) archiveFile(src, dst string) (fileMeta, error) {
	info, err := os.Stat(src)
	if err != nil {
		return fileMeta{}, err
	}
	name := filepath.Base(dst)
	if a.MaxEntryBytes > 0 && info.Size() > a.MaxEntryBytes {
		kept, err := truncateCopy(src, dst, a.MaxEntryBytes, info.Size())
		if err != nil {
			return fileMeta{}, err
		}
		return fileMeta{Name: name, Bytes: kept, OriginalBytes: info.Size(), Truncated: true}, nil
	}
	if err := copyFile(src, dst); err != nil {
		return fileMeta{}, err
	}
	return fileMeta{Name: name, Bytes: info.Size(), OriginalBytes: info.Size()}, nil
}

// truncateCopy writes the head and tail of an oversized console into dst
// with a marker between them, keeping roughly budget bytes.
func truncateCopy(src, dst string, budget, size int64) (int64, error) {
	head := headBytes
	if head > budget/2 {
		head = budget / 2
	}
	tail := budget - head

	in, err := os.Open(src)
	if err != nil {
		return 0, err
	}
	defer func() { _ = in.Close() }()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return 0, err
	}
	var written int64
	n, err := io.CopyN(out, in, head)
	written += n
	if err != nil {
		_ = out.Close()
		return written, err
	}
	m, err := fmt.Fprintf(out, "\n--- console truncated: original %d bytes, kept first %d and last %d ---\n", size, head, tail)
	written += int64(m)
	if err != nil {
		_ = out.Close()
		return written, err
	}
	if _, err := in.Seek(size-tail, io.SeekStart); err != nil {
		_ = out.Close()
		return written, err
	}
	n, err = io.Copy(out, in)
	written += n
	if err != nil {
		_ = out.Close()
		return written, err
	}
	return written, out.Close()
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}
