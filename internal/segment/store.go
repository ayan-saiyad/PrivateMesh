package segment

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"

	"github.com/ayansaiyad/privatemesh/internal/search"
)

const (
	segmentPrefix   = "segment-"
	segmentSuffix   = ".pmseg"
	temporarySuffix = ".tmp"
)

// Store publishes and reads immutable segments in one directory.
type Store struct {
	directory string
	mu        sync.Mutex
}

// NewStore opens a segment directory and removes incomplete temporary files.
func NewStore(directory string) (*Store, error) {
	if strings.TrimSpace(directory) == "" {
		return nil, errors.New("segment directory is required")
	}
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return nil, fmt.Errorf("create segment directory: %w", err)
	}
	store := &Store{directory: directory}
	if err := store.recoverLocked(); err != nil {
		return nil, err
	}
	return store, nil
}

// Publish atomically writes a new segment containing the supplied mutations.
func (s *Store) Publish(mutations []Mutation) (uint64, error) {
	if len(mutations) == 0 {
		return 0, errors.New("at least one mutation is required")
	}
	records := make([]record, 0, len(mutations))
	for _, mutation := range mutations {
		records = append(records, record{
			document: mutation.Document,
			deleted:  mutation.Deleted,
		})
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	segments, err := s.listLocked()
	if err != nil {
		return 0, err
	}
	generation := nextGeneration(segments)
	if err := s.publishLocked(generation, records); err != nil {
		return 0, err
	}
	return generation, nil
}

// Documents returns the latest visible version of every stored document.
func (s *Store) Documents() ([]search.Document, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	segments, err := s.listLocked()
	if err != nil {
		return nil, err
	}

	current := make(map[string]record)
	for _, stored := range segments {
		for _, entry := range stored.segment.records {
			current[entry.document.ID] = entry
		}
	}
	documents := make([]search.Document, 0, len(current))
	for _, entry := range current {
		if !entry.deleted {
			documents = append(documents, entry.document)
		}
	}
	slices.SortFunc(documents, func(left, right search.Document) int {
		return strings.Compare(left.ID, right.ID)
	})
	return documents, nil
}

// Compact merges old segments when their count exceeds the supplied limit.
func (s *Store) Compact(maxSegments int) (bool, error) {
	if maxSegments < 1 {
		return false, errors.New("maximum segment count must be positive")
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	segments, err := s.listLocked()
	if err != nil {
		return false, err
	}
	if len(segments) <= maxSegments {
		return false, nil
	}

	current := make(map[string]record)
	for _, stored := range segments {
		for _, entry := range stored.segment.records {
			current[entry.document.ID] = entry
		}
	}
	merged := make([]record, 0, len(current))
	for _, entry := range current {
		if !entry.deleted {
			merged = append(merged, entry)
		}
	}
	generation := nextGeneration(segments)
	if err := s.publishLocked(generation, merged); err != nil {
		return false, err
	}
	for _, stored := range segments {
		if err := os.Remove(stored.path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return false, fmt.Errorf("remove merged segment: %w", err)
		}
	}
	if err := syncDirectory(s.directory); err != nil {
		return false, err
	}
	return true, nil
}

type storedSegment struct {
	path    string
	segment fileSegment
}

func (s *Store) recoverLocked() error {
	entries, err := os.ReadDir(s.directory)
	if err != nil {
		return fmt.Errorf("read segment directory: %w", err)
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), temporarySuffix) {
			continue
		}
		if err := os.Remove(filepath.Join(s.directory, entry.Name())); err != nil {
			return fmt.Errorf("remove incomplete segment: %w", err)
		}
	}
	return nil
}

func (s *Store) listLocked() ([]storedSegment, error) {
	entries, err := os.ReadDir(s.directory)
	if err != nil {
		return nil, fmt.Errorf("read segment directory: %w", err)
	}
	segments := make([]storedSegment, 0)
	for _, entry := range entries {
		if entry.IsDir() || !isSegmentName(entry.Name()) {
			continue
		}
		path := filepath.Join(s.directory, entry.Name())
		// The filename passed a strict segment-name check and cannot traverse directories.
		//nolint:gosec
		file, err := os.Open(path)
		if err != nil {
			return nil, fmt.Errorf("open segment %q: %w", entry.Name(), err)
		}
		decoded, decodeErr := readSegment(file)
		closeErr := file.Close()
		if decodeErr != nil {
			return nil, fmt.Errorf("read segment %q: %w", entry.Name(), decodeErr)
		}
		if closeErr != nil {
			return nil, fmt.Errorf("close segment %q: %w", entry.Name(), closeErr)
		}
		if entry.Name() != segmentName(decoded.generation) {
			return nil, fmt.Errorf("%w: segment generation does not match filename", ErrInvalidSegment)
		}
		segments = append(segments, storedSegment{path: path, segment: decoded})
	}
	slices.SortFunc(segments, func(left, right storedSegment) int {
		return compareUint64(left.segment.generation, right.segment.generation)
	})
	return segments, nil
}

func (s *Store) publishLocked(generation uint64, records []record) (returnErr error) {
	temporary, err := os.CreateTemp(s.directory, ".segment-*.tmp")
	if err != nil {
		return fmt.Errorf("create temporary segment: %w", err)
	}
	temporaryPath := temporary.Name()
	defer func() {
		if returnErr != nil {
			_ = temporary.Close()
			_ = os.Remove(temporaryPath)
		}
	}()
	if err := temporary.Chmod(0o600); err != nil {
		return fmt.Errorf("set segment permissions: %w", err)
	}
	if err := writeSegment(temporary, generation, records); err != nil {
		return err
	}
	if err := temporary.Sync(); err != nil {
		return fmt.Errorf("sync temporary segment: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close temporary segment: %w", err)
	}
	finalPath := filepath.Join(s.directory, segmentName(generation))
	if err := os.Rename(temporaryPath, finalPath); err != nil {
		return fmt.Errorf("publish segment: %w", err)
	}
	if err := syncDirectory(s.directory); err != nil {
		return err
	}
	return nil
}

func nextGeneration(segments []storedSegment) uint64 {
	if len(segments) == 0 {
		return 1
	}
	return segments[len(segments)-1].segment.generation + 1
}

func segmentName(generation uint64) string {
	return segmentPrefix + fmt.Sprintf("%020d", generation) + segmentSuffix
}

func isSegmentName(name string) bool {
	if !strings.HasPrefix(name, segmentPrefix) || !strings.HasSuffix(name, segmentSuffix) {
		return false
	}
	rawGeneration := strings.TrimSuffix(strings.TrimPrefix(name, segmentPrefix), segmentSuffix)
	if len(rawGeneration) != 20 {
		return false
	}
	_, err := strconv.ParseUint(rawGeneration, 10, 64)
	return err == nil
}

func syncDirectory(directory string) error {
	// The directory is the configured root of this store.
	//nolint:gosec
	handle, err := os.Open(directory)
	if err != nil {
		return fmt.Errorf("open segment directory for sync: %w", err)
	}
	defer func() {
		_ = handle.Close()
	}()
	if err := handle.Sync(); err != nil {
		return fmt.Errorf("sync segment directory: %w", err)
	}
	return nil
}

func compareUint64(left, right uint64) int {
	switch {
	case left < right:
		return -1
	case left > right:
		return 1
	default:
		return 0
	}
}
