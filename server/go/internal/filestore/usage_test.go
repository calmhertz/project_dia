package filestore_test

import (
	"errors"
	"math"
	"testing"

	"aagasa/internal/filestore"
)


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

	err = store.EnsureSpaceFor(math.MaxInt64 / 2)
	if !errors.Is(err, filestore.ErrNoSpace) {
		t.Fatalf("EnsureSpaceFor(huge) = %v, want ErrNoSpace", err)
	}
	if err.Error() == "" || len(err.Error()) < len("not enough free space") {
		t.Errorf("message = %q", err.Error())
	}

	if err := store.EnsureSpaceFor(1024); err != nil {
		t.Errorf("EnsureSpaceFor(1 KiB) = %v, want nil", err)
	}
}

func TestNegativeSizeIsTreatedAsZero(t *testing.T) {
	store, err := filestore.New(t.TempDir(), t.TempDir())
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	if err := store.EnsureSpaceFor(-1 << 40); err != nil {
		t.Errorf("EnsureSpaceFor(negative) = %v, want nil", err)
	}
}

func TestAnUnmeasurableFilesystemDoesNotRefuseWrites(t *testing.T) {
	store, err := filestore.New(t.TempDir()+"/gone", t.TempDir())
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	if err := store.EnsureSpaceFor(1024); err != nil {
		t.Errorf("EnsureSpaceFor on a fresh root = %v, want nil", err)
	}
}
