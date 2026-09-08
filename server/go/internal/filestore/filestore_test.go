package filestore

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func newStore(t *testing.T) *Store {
	t.Helper()
	root := t.TempDir()
	store, err := New(filepath.Join(root, "recordings"), filepath.Join(root, "pipelines"))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return store
}

func TestRecordingPathIsUnderRoot(t *testing.T) {
	store := newStore(t)

	path, err := store.RecordingPath("station-1", "pass-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.HasPrefix(path, store.RecordingsRoot()+string(os.PathSeparator)) {
		t.Errorf("path %q is not under root %q", path, store.RecordingsRoot())
	}
}

// A crafted identifier must never reach outside the configured root.
func TestTraversalIsRejected(t *testing.T) {
	store := newStore(t)

	hostile := []string{
		"..",
		"../../etc",
		"a/../../b",
		"station/../..",
		"/etc/passwd",
		"foo/bar",
		`..\windows`,
		"",
		".hidden",
		strings.Repeat("a", 200),
	}

	for _, segment := range hostile {
		t.Run(segment, func(t *testing.T) {
			if _, err := store.RecordingPath(segment, "pass-1"); !errors.Is(err, ErrUnsafePath) {
				t.Errorf("RecordingPath(%q) error = %v, want ErrUnsafePath", segment, err)
			}
			if _, err := store.RecordingPath("station-1", segment); !errors.Is(err, ErrUnsafePath) {
				t.Errorf("RecordingPath(pass=%q) error = %v, want ErrUnsafePath", segment, err)
			}
			if _, err := store.PipelinePath(segment); !errors.Is(err, ErrUnsafePath) {
				t.Errorf("PipelinePath(%q) error = %v, want ErrUnsafePath", segment, err)
			}
		})
	}
}

func TestEnsureRecordingDirIsOwnerOnly(t *testing.T) {
	store := newStore(t)

	path, err := store.EnsureRecordingDir("station-1", "pass-1")
	if err != nil {
		t.Fatalf("EnsureRecordingDir: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if mode := info.Mode().Perm(); mode != 0o700 {
		t.Errorf("mode = %o, want 700", mode)
	}
}

func TestWritePipelineStoresDefinition(t *testing.T) {
	store := newStore(t)
	definition := []byte(`{"name":"test"}`)

	path, err := store.WritePipeline("pipeline-1", definition)
	if err != nil {
		t.Fatalf("WritePipeline: %v", err)
	}

	stored, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if string(stored) != string(definition) {
		t.Errorf("stored %q, want %q", stored, definition)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if mode := info.Mode().Perm(); mode != 0o600 {
		t.Errorf("mode = %o, want 600", mode)
	}
}
