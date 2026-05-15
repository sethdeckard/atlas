package atomicfile_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sethdeckard/atlas/internal/atomicfile"
)

func TestWrite_ReplacesExistingFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "out.txt")
	if err := os.WriteFile(path, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := atomicfile.Write(path, []byte("new"), atomicfile.Options{TempPattern: "x-*.txt"}); err != nil {
		t.Fatalf("Write: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "new" {
		t.Errorf("got %q; want %q", got, "new")
	}
	// Tempfile must be cleaned up on success.
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "x-") {
			t.Errorf("leftover tempfile %s", e.Name())
		}
	}
}

func TestWrite_CreatesNewFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "fresh.txt")
	if err := atomicfile.Write(path, []byte("hi"), atomicfile.Options{}); err != nil {
		t.Fatalf("Write: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil || string(got) != "hi" {
		t.Errorf("got %q err=%v; want %q/nil", got, err, "hi")
	}
}

func TestWrite_MkdirParent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nested", "deep", "out.txt")
	if err := atomicfile.Write(path, []byte("ok"), atomicfile.Options{MkdirParent: true}); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Errorf("file not created: %v", err)
	}
}

func TestWrite_NoMkdirParent_FailsOnMissingDir(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nope", "out.txt")
	if err := atomicfile.Write(path, []byte("x"), atomicfile.Options{}); err == nil {
		t.Errorf("expected error when parent dir is missing and MkdirParent=false")
	}
}

func TestWrite_DefaultTempPattern(t *testing.T) {
	// Empty TempPattern should still succeed (helper picks a default).
	dir := t.TempDir()
	path := filepath.Join(dir, "default.txt")
	if err := atomicfile.Write(path, []byte("ok"), atomicfile.Options{}); err != nil {
		t.Fatalf("Write: %v", err)
	}
}
