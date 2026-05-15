// Package atomicfile is the shared tempfile-plus-rename primitive atlas
// uses to make cache, config, and export writes atomic. Callers pick
// the temp pattern (so failed writes leave recognizable debris) and
// whether the parent directory should be created if missing.
package atomicfile

import (
	"fmt"
	"os"
	"path/filepath"
)

// Options tunes a Write call.
type Options struct {
	// TempPattern is the os.CreateTemp pattern used for the staging
	// file, e.g. "cache-*.json". Empty falls back to "atlas-*".
	TempPattern string
	// MkdirParent creates filepath.Dir(path) with 0o755 before the
	// staging write. Use it when the caller can't guarantee the
	// directory already exists (e.g. first-run cache writes, export
	// targets pointing at a fresh path).
	MkdirParent bool
}

// Write replaces path with data atomically: the data is staged into a
// tempfile in the same directory, then renamed into place. A failed
// write or rename cleans up the tempfile so partial state never leaks.
//
// All callers (cache.Save, config.atomicWrite, cli/export.atomicWrite)
// share this so the atomicity contract is identical across atlas's
// three write paths.
func Write(path string, data []byte, opts Options) error {
	dir := filepath.Dir(path)
	if opts.MkdirParent {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("mkdir %s: %w", dir, err)
		}
	}
	pattern := opts.TempPattern
	if pattern == "" {
		pattern = "atlas-*"
	}
	tmp, err := os.CreateTemp(dir, pattern)
	if err != nil {
		return fmt.Errorf("create tempfile: %w", err)
	}
	tmpPath := tmp.Name()
	cleanup := func() { _ = os.Remove(tmpPath) }
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		cleanup()
		return fmt.Errorf("write: %w", err)
	}
	if err := tmp.Close(); err != nil {
		cleanup()
		return fmt.Errorf("close: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		cleanup()
		return fmt.Errorf("rename: %w", err)
	}
	return nil
}
