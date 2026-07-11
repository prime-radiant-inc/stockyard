package consolearchive

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Prune enforces the archive's retention bounds: stale staging directories,
// entries older than MaxAge, then oldest-first removal until the archive
// fits MaxTotalBytes. The newest entry always survives the byte bound.
func (a *Archiver) Prune() (err error) {
	defer func() {
		if r := recover(); r != nil {
			a.logf("console_archive_prune_panic err=%v", r)
			err = fmt.Errorf("console archive prune panicked: %v", r)
		}
	}()
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.pruneLocked()
}

type entryInfo struct {
	name    string
	modTime time.Time
	bytes   int64
}

func (a *Archiver) pruneLocked() error {
	dirents, err := os.ReadDir(a.Dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		a.logf("console_archive_prune_failed err=%v", err)
		return err
	}

	now := time.Now()
	removed := 0
	var removedBytes int64
	var entries []entryInfo

	for _, de := range dirents {
		if !de.IsDir() {
			continue
		}
		info, err := de.Info()
		if err != nil {
			continue
		}
		name := de.Name()
		path := filepath.Join(a.Dir, name)
		if strings.HasPrefix(name, stagingPrefix) {
			if !validArchiveEntryName(strings.TrimPrefix(name, stagingPrefix)) {
				continue
			}
			if now.Sub(info.ModTime()) > stagingMaxAge {
				bytes := dirBytes(path)
				if err := os.RemoveAll(path); err == nil {
					removed++
					removedBytes += bytes
				}
			}
			continue
		}
		if strings.HasPrefix(name, ".") || !validArchiveEntryName(name) {
			continue
		}
		entries = append(entries, entryInfo{name: name, modTime: info.ModTime(), bytes: dirBytes(path)})
	}

	// Entry names begin with a UTC timestamp, so name order is age order.
	sort.Slice(entries, func(i, j int) bool { return entries[i].name < entries[j].name })

	var kept []entryInfo
	for _, e := range entries {
		if a.MaxAge > 0 && now.Sub(e.modTime) > a.MaxAge {
			if err := os.RemoveAll(filepath.Join(a.Dir, e.name)); err == nil {
				removed++
				removedBytes += e.bytes
				continue
			}
		}
		kept = append(kept, e)
	}

	var total int64
	for _, e := range kept {
		total += e.bytes
	}
	for i := 0; a.MaxTotalBytes > 0 && total > a.MaxTotalBytes && len(kept)-i > 1; i++ {
		e := kept[i]
		if err := os.RemoveAll(filepath.Join(a.Dir, e.name)); err != nil {
			continue
		}
		total -= e.bytes
		removed++
		removedBytes += e.bytes
	}

	if removed > 0 {
		a.logf("console_archive_pruned entries=%d bytes=%d", removed, removedBytes)
	}
	return nil
}

func dirBytes(dir string) int64 {
	var total int64
	_ = filepath.WalkDir(dir, func(_ string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if info, err := d.Info(); err == nil {
			total += info.Size()
		}
		return nil
	})
	return total
}
