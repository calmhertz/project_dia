package filestore_test

import (
	"errors"
	"math"
	"testing"

	"aagasa/internal/filestore"
)

// A ground station fills its disk with recordings and nothing else, so the
// Server has to refuse before a write fails halfway through.

func TestUsageReportsTheRecordingFilesystem(t *testing.T) {
	store, err := filestore.New(t.TempDir(), t.TempDir())
	if err != nil {
		t.Fatalf("new store: %v", err)
	}

	usage, err := store.RecordingsUsage()
	if err != nil {
		t.Fatalf("usage: %v", err)
	}
	if usage.TotalBytes == 0 {
		t.Fatal("total capacity reads as zero")
	}
	if usage.FreeBytes > usage.TotalBytes {
		t.Errorf("free %d exceeds total %d", usage.FreeBytes, usage.TotalBytes)
	}
	if usage.UsedPercentage < 0 || usage.UsedPercentage > 100 {
		t.Errorf("used = %.1f%%", usage.UsedPercentage)
	}
}

func TestSpaceIsRefusedForAWriteThatWouldNotFit(t *testing.T) {
	store, err := filestore.New(t.TempDir(), t.TempDir())
	if err != nil {
		t.Fatalf("new store: %v", err)
	}

	// A write far larger than any disk cannot be satisfied.
	err = store.EnsureSpaceFor(math.MaxInt64 / 2)
	if !errors.Is(err, filestore.ErrNoSpace) {
		t.Fatalf("EnsureSpaceFor(huge) = %v, want ErrNoSpace", err)
	}
	// The message has to say how short it is, or an operator cannot act.
	if err.Error() == "" || len(err.Error()) < len("not enough free space") {
		t.Errorf("message = %q", err.Error())
	}

	// A small write on a working filesystem is fine.
	if err := store.EnsureSpaceFor(1024); err != nil {
		t.Errorf("EnsureSpaceFor(1 KiB) = %v, want nil", err)
	}
}

// A negative size is nonsense, and must not be treated as free headroom.
func TestNegativeSizeIsTreatedAsZero(t *testing.T) {
	store, err := filestore.New(t.TempDir(), t.TempDir())
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	if err := store.EnsureSpaceFor(-1 << 40); err != nil {
		t.Errorf("EnsureSpaceFor(negative) = %v, want nil", err)
	}
}

// Being unable to measure must not stop uploads: the write itself will fail
// if it must, and refusing everything over a failed measurement is worse.
func TestAnUnmeasurableFilesystemDoesNotRefuseWrites(t *testing.T) {
	store, err := filestore.New(t.TempDir()+"/gone", t.TempDir())
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	if err := store.EnsureSpaceFor(1024); err != nil {
		t.Errorf("EnsureSpaceFor on a fresh root = %v, want nil", err)
	}
}
