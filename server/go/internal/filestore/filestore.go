package filestore

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

var ErrUnsafePath = errors.New("unsafe path")

const (
	dirMode  os.FileMode = 0o700
	fileMode os.FileMode = 0o600
)

var safeSegment = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)

type Store struct {
	recordingsRoot string
	pipelinesRoot  string
}

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

func (s *Store) RecordingsRoot() string { return s.recordingsRoot }

func (s *Store) PipelinesRoot() string { return s.pipelinesRoot }

func (s *Store) RecordingPath(stationID, passID string) (string, error) {
	return joinSafe(s.recordingsRoot, stationID, passID)
}

func (s *Store) PipelinePath(pipelineID string) (string, error) {
	return joinSafe(s.pipelinesRoot, pipelineID+".json")
}

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


const ReserveBytes int64 = 1 << 30

var ErrNoSpace = errors.New("not enough free space for the recording")

type Usage struct {
	TotalBytes     uint64
	FreeBytes      uint64
	UsedPercentage float64
}

func (s *Store) RecordingsUsage() (Usage, error) {
	return usageOf(s.recordingsRoot)
}

func (s *Store) EnsureSpaceFor(size int64) error {
	usage, err := s.RecordingsUsage()
	if err != nil {
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
