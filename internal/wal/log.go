// Package wal provides durable document mutation logging and recovery.
package wal

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/ayansaiyad/privatemesh/internal/search"
	"github.com/ayansaiyad/privatemesh/internal/segment"
)

const (
	fileMagic            = "PMWAL001"
	formatVersion        = uint16(1)
	headerSize           = 20
	recordHeaderSize     = 8
	maxRecordSize        = 32 << 20
	recordUpsert         = byte(1)
	recordDelete         = byte(2)
	recordCommit         = byte(3)
	filePermissions      = 0o600
	directoryPermissions = 0o700
)

var (
	// ErrCorruptLog indicates that a complete WAL record failed validation.
	ErrCorruptLog = errors.New("corrupt write-ahead log")
	// ErrUnsupportedVersion indicates that a WAL uses an unknown file format.
	ErrUnsupportedVersion = errors.New("unsupported write-ahead log version")
)

var checksumTable = crc32.MakeTable(crc32.Castagnoli)

// Entry is a sequenced document mutation recovered from the log.
type Entry struct {
	Sequence uint64
	Mutation segment.Mutation
}

// Log appends, commits, replays, and truncates document mutations.
type Log struct {
	mu                sync.Mutex
	path              string
	file              *os.File
	baseSequence      uint64
	lastSequence      uint64
	committedSequence uint64
}

// Open creates or recovers a write-ahead log at path.
func Open(path string) (*Log, error) {
	if strings.TrimSpace(path) == "" {
		return nil, errors.New("write-ahead log path is required")
	}
	if err := os.MkdirAll(filepath.Dir(path), directoryPermissions); err != nil {
		return nil, fmt.Errorf("create write-ahead log directory: %w", err)
	}
	file, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, filePermissions) //nolint:gosec // The caller selects the storage path.
	if err != nil {
		return nil, fmt.Errorf("open write-ahead log: %w", err)
	}
	log := &Log{path: path, file: file}
	if err := log.initialize(); err != nil {
		_ = file.Close()
		return nil, err
	}
	return log, nil
}

// Append records a mutation and returns its sequence number.
func (l *Log) Append(mutation segment.Mutation) (uint64, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.file == nil {
		return 0, errors.New("write-ahead log is closed")
	}
	mutation.Document.ID = strings.TrimSpace(mutation.Document.ID)
	if mutation.Document.ID == "" {
		return 0, errors.New("document ID is required")
	}
	if l.lastSequence == ^uint64(0) {
		return 0, errors.New("write-ahead log sequence exhausted")
	}

	sequence := l.lastSequence + 1
	payload, err := encodeMutation(sequence, mutation)
	if err != nil {
		return 0, err
	}
	if err := appendRecord(l.file, payload); err != nil {
		return 0, err
	}
	l.lastSequence = sequence
	return sequence, nil
}

// Commit makes every mutation through sequence durable.
func (l *Log) Commit(sequence uint64) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.file == nil {
		return errors.New("write-ahead log is closed")
	}
	if sequence < l.committedSequence || sequence > l.lastSequence {
		return errors.New("commit sequence is outside the pending range")
	}
	if sequence == l.committedSequence {
		return l.file.Sync()
	}
	payload := encodeCommit(sequence)
	if err := appendRecord(l.file, payload); err != nil {
		return err
	}
	if err := l.file.Sync(); err != nil {
		return fmt.Errorf("sync write-ahead log: %w", err)
	}
	l.committedSequence = sequence
	return nil
}

// Replay returns committed mutations in sequence order.
func (l *Log) Replay(ctx context.Context) ([]Entry, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.file == nil {
		return nil, errors.New("write-ahead log is closed")
	}
	scan, err := scanLog(ctx, l.file)
	if err != nil {
		return nil, err
	}
	entries := make([]Entry, 0, len(scan.entries))
	for _, entry := range scan.entries {
		if entry.Sequence <= scan.committedSequence {
			entries = append(entries, entry)
		}
	}
	return entries, nil
}

// Truncate discards mutations through a durable checkpoint sequence.
func (l *Log) Truncate(sequence uint64) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.file == nil {
		return errors.New("write-ahead log is closed")
	}
	if sequence < l.baseSequence || sequence > l.committedSequence {
		return errors.New("truncate sequence is outside the committed range")
	}

	scan, err := scanLog(context.Background(), l.file)
	if err != nil {
		return err
	}
	remaining := make([]Entry, 0, len(scan.entries))
	for _, entry := range scan.entries {
		if entry.Sequence > sequence {
			remaining = append(remaining, entry)
		}
	}
	if err := l.rewrite(sequence, remaining, scan.committedSequence); err != nil {
		return err
	}
	l.baseSequence = sequence
	l.lastSequence = scan.lastSequence
	l.committedSequence = scan.committedSequence
	return nil
}

// Close flushes and closes the log.
func (l *Log) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.file == nil {
		return nil
	}
	if err := l.file.Close(); err != nil {
		return fmt.Errorf("close write-ahead log: %w", err)
	}
	l.file = nil
	return nil
}

func (l *Log) initialize() error {
	info, err := l.file.Stat()
	if err != nil {
		return fmt.Errorf("stat write-ahead log: %w", err)
	}
	if info.Size() == 0 {
		if err := writeHeader(l.file, 0); err != nil {
			return err
		}
		if err := l.file.Sync(); err != nil {
			return fmt.Errorf("sync write-ahead log header: %w", err)
		}
	}

	scan, err := scanLog(context.Background(), l.file)
	if err != nil {
		return err
	}
	if scan.incompleteTail {
		if err := l.file.Truncate(scan.validOffset); err != nil {
			return fmt.Errorf("truncate incomplete write-ahead log record: %w", err)
		}
		if err := l.file.Sync(); err != nil {
			return fmt.Errorf("sync recovered write-ahead log: %w", err)
		}
	}
	if _, err := l.file.Seek(0, io.SeekEnd); err != nil {
		return fmt.Errorf("seek to write-ahead log end: %w", err)
	}
	l.baseSequence = scan.baseSequence
	l.lastSequence = scan.lastSequence
	l.committedSequence = scan.committedSequence
	return nil
}

func (l *Log) rewrite(baseSequence uint64, entries []Entry, committedSequence uint64) (returnErr error) {
	directory := filepath.Dir(l.path)
	temporary, err := os.CreateTemp(directory, ".wal-*.tmp")
	if err != nil {
		return fmt.Errorf("create temporary write-ahead log: %w", err)
	}
	temporaryPath := temporary.Name()
	defer func() {
		if returnErr != nil {
			_ = temporary.Close()
			_ = os.Remove(temporaryPath)
		}
	}()
	if err := temporary.Chmod(filePermissions); err != nil {
		return fmt.Errorf("set write-ahead log permissions: %w", err)
	}
	if err := writeHeader(temporary, baseSequence); err != nil {
		return err
	}
	for _, entry := range entries {
		payload, err := encodeMutation(entry.Sequence, entry.Mutation)
		if err != nil {
			return err
		}
		if err := appendRecord(temporary, payload); err != nil {
			return err
		}
	}
	if committedSequence > baseSequence {
		if err := appendRecord(temporary, encodeCommit(committedSequence)); err != nil {
			return err
		}
	}
	if err := temporary.Sync(); err != nil {
		return fmt.Errorf("sync temporary write-ahead log: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close temporary write-ahead log: %w", err)
	}
	if err := l.file.Close(); err != nil {
		return fmt.Errorf("close old write-ahead log: %w", err)
	}
	if err := os.Rename(temporaryPath, l.path); err != nil {
		return fmt.Errorf("replace write-ahead log: %w", err)
	}
	if err := syncDirectory(directory); err != nil {
		return err
	}
	reopened, err := os.OpenFile(l.path, os.O_RDWR, filePermissions) //nolint:gosec // Reopening the configured WAL path.
	if err != nil {
		return fmt.Errorf("reopen write-ahead log: %w", err)
	}
	l.file = reopened
	if _, err := l.file.Seek(0, io.SeekEnd); err != nil {
		return fmt.Errorf("seek to write-ahead log end: %w", err)
	}
	return nil
}

type scanResult struct {
	baseSequence      uint64
	lastSequence      uint64
	committedSequence uint64
	validOffset       int64
	incompleteTail    bool
	entries           []Entry
}

func scanLog(ctx context.Context, file *os.File) (scanResult, error) {
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return scanResult{}, fmt.Errorf("seek to write-ahead log start: %w", err)
	}
	header := make([]byte, headerSize)
	if _, err := io.ReadFull(file, header); err != nil {
		return scanResult{}, fmt.Errorf("%w: read header: %w", ErrCorruptLog, err)
	}
	if string(header[:8]) != fileMagic {
		return scanResult{}, fmt.Errorf("%w: incorrect magic", ErrCorruptLog)
	}
	version := binary.BigEndian.Uint16(header[8:10])
	if version != formatVersion {
		return scanResult{}, fmt.Errorf("%w: %d", ErrUnsupportedVersion, version)
	}
	if size := binary.BigEndian.Uint16(header[10:12]); size != headerSize {
		return scanResult{}, fmt.Errorf("%w: incorrect header size", ErrCorruptLog)
	}

	result := scanResult{
		baseSequence: binary.BigEndian.Uint64(header[12:20]),
		validOffset:  headerSize,
	}
	result.lastSequence = result.baseSequence
	result.committedSequence = result.baseSequence

	for {
		if err := ctx.Err(); err != nil {
			return scanResult{}, err
		}
		recordHeader := make([]byte, recordHeaderSize)
		count, err := io.ReadFull(file, recordHeader)
		if errors.Is(err, io.EOF) && count == 0 {
			break
		}
		if errors.Is(err, io.ErrUnexpectedEOF) {
			result.incompleteTail = true
			break
		}
		if err != nil {
			return scanResult{}, fmt.Errorf("read write-ahead log record header: %w", err)
		}
		payloadSize := binary.BigEndian.Uint32(recordHeader[:4])
		if payloadSize == 0 || payloadSize > maxRecordSize {
			return scanResult{}, fmt.Errorf("%w: invalid record size", ErrCorruptLog)
		}
		payload := make([]byte, payloadSize)
		if _, err := io.ReadFull(file, payload); errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, io.EOF) {
			result.incompleteTail = true
			break
		} else if err != nil {
			return scanResult{}, fmt.Errorf("read write-ahead log record: %w", err)
		}
		expectedChecksum := binary.BigEndian.Uint32(recordHeader[4:8])
		if crc32.Checksum(payload, checksumTable) != expectedChecksum {
			return scanResult{}, ErrCorruptLog
		}
		kind, sequence, mutation, err := decodeRecord(payload)
		if err != nil {
			return scanResult{}, err
		}
		switch kind {
		case recordUpsert, recordDelete:
			if sequence != result.lastSequence+1 {
				return scanResult{}, fmt.Errorf("%w: non-contiguous mutation sequence", ErrCorruptLog)
			}
			result.entries = append(result.entries, Entry{Sequence: sequence, Mutation: mutation})
			result.lastSequence = sequence
		case recordCommit:
			if sequence < result.committedSequence || sequence > result.lastSequence {
				return scanResult{}, fmt.Errorf("%w: invalid commit sequence", ErrCorruptLog)
			}
			result.committedSequence = sequence
		default:
			return scanResult{}, fmt.Errorf("%w: unknown record kind", ErrCorruptLog)
		}
		result.validOffset += int64(recordHeaderSize) + int64(payloadSize)
	}
	return result, nil
}

func writeHeader(output io.Writer, baseSequence uint64) error {
	header := make([]byte, headerSize)
	copy(header[:8], fileMagic)
	binary.BigEndian.PutUint16(header[8:10], formatVersion)
	binary.BigEndian.PutUint16(header[10:12], headerSize)
	binary.BigEndian.PutUint64(header[12:20], baseSequence)
	if _, err := output.Write(header); err != nil {
		return fmt.Errorf("write write-ahead log header: %w", err)
	}
	return nil
}

func appendRecord(output io.Writer, payload []byte) error {
	if len(payload) == 0 || len(payload) > maxRecordSize {
		return errors.New("write-ahead log record exceeds size limit")
	}
	header := make([]byte, recordHeaderSize)
	// The payload length is bounded to 32 MiB above.
	//nolint:gosec
	binary.BigEndian.PutUint32(header[:4], uint32(len(payload)))
	binary.BigEndian.PutUint32(header[4:8], crc32.Checksum(payload, checksumTable))
	if _, err := output.Write(header); err != nil {
		return fmt.Errorf("write record header: %w", err)
	}
	if _, err := output.Write(payload); err != nil {
		return fmt.Errorf("write record payload: %w", err)
	}
	return nil
}

func encodeMutation(sequence uint64, mutation segment.Mutation) ([]byte, error) {
	if strings.TrimSpace(mutation.Document.ID) == "" {
		return nil, errors.New("document ID is required")
	}
	remaining := maxRecordSize - 32
	for _, value := range []string{
		mutation.Document.ID,
		mutation.Document.Title,
		mutation.Document.Body,
	} {
		if len(value) > remaining {
			return nil, errors.New("write-ahead log record exceeds size limit")
		}
		remaining -= len(value)
	}
	var payload bytes.Buffer
	if mutation.Deleted {
		payload.WriteByte(recordDelete)
	} else {
		payload.WriteByte(recordUpsert)
	}
	writeUint64(&payload, sequence)
	writeString(&payload, mutation.Document.ID)
	if !mutation.Deleted {
		writeString(&payload, mutation.Document.Title)
		writeString(&payload, mutation.Document.Body)
	}
	if payload.Len() > maxRecordSize {
		return nil, errors.New("write-ahead log record exceeds size limit")
	}
	return payload.Bytes(), nil
}

func encodeCommit(sequence uint64) []byte {
	payload := make([]byte, 9)
	payload[0] = recordCommit
	binary.BigEndian.PutUint64(payload[1:], sequence)
	return payload
}

func decodeRecord(payload []byte) (byte, uint64, segment.Mutation, error) {
	if len(payload) < 9 {
		return 0, 0, segment.Mutation{}, fmt.Errorf("%w: record is too short", ErrCorruptLog)
	}
	kind := payload[0]
	sequence := binary.BigEndian.Uint64(payload[1:9])
	if sequence == 0 {
		return 0, 0, segment.Mutation{}, fmt.Errorf("%w: zero sequence", ErrCorruptLog)
	}
	if kind == recordCommit {
		if len(payload) != 9 {
			return 0, 0, segment.Mutation{}, fmt.Errorf("%w: malformed commit record", ErrCorruptLog)
		}
		return kind, sequence, segment.Mutation{}, nil
	}
	if kind != recordUpsert && kind != recordDelete {
		return 0, 0, segment.Mutation{}, fmt.Errorf("%w: unknown mutation kind", ErrCorruptLog)
	}
	reader := bytes.NewReader(payload[9:])
	id, err := readString(reader)
	if err != nil {
		return 0, 0, segment.Mutation{}, err
	}
	document := search.Document{ID: id}
	deleted := kind == recordDelete
	if !deleted {
		document.Title, err = readString(reader)
		if err != nil {
			return 0, 0, segment.Mutation{}, err
		}
		document.Body, err = readString(reader)
		if err != nil {
			return 0, 0, segment.Mutation{}, err
		}
	}
	if reader.Len() != 0 || strings.TrimSpace(document.ID) == "" {
		return 0, 0, segment.Mutation{}, fmt.Errorf("%w: malformed mutation record", ErrCorruptLog)
	}
	return kind, sequence, segment.Mutation{Document: document, Deleted: deleted}, nil
}

func writeString(output *bytes.Buffer, value string) {
	var length [binary.MaxVarintLen64]byte
	count := binary.PutUvarint(length[:], uint64(len(value)))
	output.Write(length[:count])
	output.WriteString(value)
}

func readString(input *bytes.Reader) (string, error) {
	length, err := binary.ReadUvarint(input)
	if err != nil || length > maxRecordSize {
		return "", fmt.Errorf("%w: invalid string length", ErrCorruptLog)
	}
	value := make([]byte, length)
	if _, err := io.ReadFull(input, value); err != nil {
		return "", fmt.Errorf("%w: read string: %w", ErrCorruptLog, err)
	}
	return string(value), nil
}

func writeUint64(output *bytes.Buffer, value uint64) {
	var encoded [8]byte
	binary.BigEndian.PutUint64(encoded[:], value)
	output.Write(encoded[:])
}

func syncDirectory(directory string) error {
	handle, err := os.Open(directory) //nolint:gosec // The directory contains the configured WAL path.
	if err != nil {
		return fmt.Errorf("open write-ahead log directory for sync: %w", err)
	}
	defer func() {
		_ = handle.Close()
	}()
	if err := handle.Sync(); err != nil {
		return fmt.Errorf("sync write-ahead log directory: %w", err)
	}
	return nil
}
