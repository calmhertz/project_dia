// Package filestore lays out the Server's on-disk content.
//
// Large binary content lives on the filesystem while its metadata lives in the
// database (spec.md section 4.3). Every path is confined to the configured
// roots so a crafted identifier cannot escape them (spec.md section 21).
package filestore

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// ErrUnsafePath is returned when an identifier would escape its root.
var ErrUnsafePath = errors.New("unsafe path")

// Directories are owner-only: recordings may belong to private passes.
const (
	dirMode  os.FileMode = 0o700
	fileMode os.FileMode = 0o600
)

// safeSegment allows only characters that cannot form traversal or separators.
var safeSegment = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)

// Store owns the recording and pipeline directory trees.
type Store struct {
	recordingsRoot string
	pipelinesRoot  string
}

// New prepares the storage roots.
func New(recordingsRoot, pipelinesRoot string) (*Store, error) {
	recordings, err := filepath.Abs(recordingsRoot)
	if err != nil {
		return nil, fmt.Errorf("resolve recordings root: %w", err)
	}
	pipelines, err := filepath.Abs(pipelinesRoot)
	if err != nil {
		return nil, fmt.Errorf("resolve pipelines root: %w", err)
	}
	for _, root := range []string{recordings, pipelines} {
		if err := os.MkdirAll(root, dirMode); err != nil {
			return nil, fmt.Errorf("create %s: %w", root, err)
		}
	}
	return &Store{recordingsRoot: recordings, pipelinesRoot: pipelines}, nil
}

// RecordingsRoot is the base directory for recording content.
func (s *Store) RecordingsRoot() string { return s.recordingsRoot }

// PipelinesRoot is the base directory for uploaded pipeline JSON.
func (s *Store) PipelinesRoot() string { return s.pipelinesRoot }

// RecordingPath returns the directory holding one pass's recording content,
// laid out as <root>/<station_id>/<pass_id>.
func (s *Store) RecordingPath(stationID, passID string) (string, error) {
	return joinSafe(s.recordingsRoot, stationID, passID)
}

// PipelinePath returns the file path for a stored custom pipeline definition.
func (s *Store) PipelinePath(pipelineID string) (string, error) {
	return joinSafe(s.pipelinesRoot, pipelineID+".json")
}

// RecordingFilePath returns where one uploaded file belongs.
//
// The relative path may contain directories, so each element is vetted
// separately and the result is confirmed to stay under the root.
func (s *Store) RecordingFilePath(stationID, passID, relativePath string) (string, error) {
	if relativePath == "" {
		return "", fmt.Errorf("%w: empty relative path", ErrUnsafePath)
	}
	segments := []string{stationID, passID}
	for _, element := range strings.Split(filepath.ToSlash(relativePath), "/") {
		if element == "" {
			return "", fmt.Errorf("%w: %q", ErrUnsafePath, relativePath)
		}
		segments = append(segments, element)
	}
	return joinSafe(s.recordingsRoot, segments...)
}

// EnsureRecordingDir creates and returns a pass's recording directory.
func (s *Store) EnsureRecordingDir(stationID, passID string) (string, error) {
	path, err := s.RecordingPath(stationID, passID)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(path, dirMode); err != nil {
		return "", fmt.Errorf("create recording directory: %w", err)
	}
	return path, nil
}

// WritePipeline stores a validated pipeline definition.
func (s *Store) WritePipeline(pipelineID string, definition []byte) (string, error) {
	path, err := s.PipelinePath(pipelineID)
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(path, definition, fileMode); err != nil {
		return "", fmt.Errorf("write pipeline: %w", err)
	}
	return path, nil
}

// joinSafe builds a path from vetted segments and confirms the result stays
// inside root even after symlink-free cleaning.
func joinSafe(root string, segments ...string) (string, error) {
	for _, segment := range segments {
		name := strings.TrimSuffix(segment, ".json")
		if !safeSegment.MatchString(name) {
			return "", fmt.Errorf("%w: %q", ErrUnsafePath, segment)
		}
	}
	path := filepath.Join(append([]string{root}, segments...)...)
	if path != root && !strings.HasPrefix(path, root+string(os.PathSeparator)) {
		return "", fmt.Errorf("%w: %q escapes root", ErrUnsafePath, path)
	}
	return path, nil
}

// Storage pressure -----------------------------------------------------------
//
// A ground station fills its disk with recordings and nothing else, so
// running out is a matter of when. The Server has to notice before a write
// fails halfway through and leaves a partial file behind.

// ReserveBytes is the headroom kept free beyond whatever a write needs.
//
// A gigabyte is enough that the database, the logs and a retry all still have
// somewhere to go once uploads start being refused.
const ReserveBytes int64 = 1 << 30

// ErrNoSpace means the write was refused because the disk is too full.
//
// The Worker keeps its local copy and retries, so refusing is the safe
// outcome (worker-spec section 15).
var ErrNoSpace = errors.New("not enough free space for the recording")

// Usage reports the recording filesystem's capacity.
type Usage struct {
	TotalBytes     uint64
	FreeBytes      uint64
	UsedPercentage float64
}

// RecordingsUsage reports free space where recordings are written.
func (s *Store) RecordingsUsage() (Usage, error) {
	return usageOf(s.recordingsRoot)
}

// EnsureSpaceFor reports whether a write of size bytes can proceed while
// leaving the reserve intact.
func (s *Store) EnsureSpaceFor(size int64) error {
	usage, err := s.RecordingsUsage()
	if err != nil {
		// Unable to tell: allow the write rather than refusing uploads over a
		// failed measurement, and let the write itself fail if it must.
		return nil
	}
	if size < 0 {
		size = 0
	}
	if usage.FreeBytes < uint64(size)+uint64(ReserveBytes) {
		return fmt.Errorf("%w: %d bytes free, %d needed plus a %d reserve",
			ErrNoSpace, usage.FreeBytes, size, ReserveBytes)
	}
	return nil
}
