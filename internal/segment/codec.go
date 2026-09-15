// Package segment stores immutable search-index segments.
package segment

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"

	"github.com/ayansaiyad/privatemesh/internal/search"
)

const (
	fileMagic      = "PMSGMNT1"
	formatVersion  = uint16(1)
	headerSize     = 60
	maxPayloadSize = 512 << 20
	maxStringSize  = 16 << 20
	maxRecordCount = 10_000_000
	maxTermCount   = 50_000_000
	deleteFlag     = byte(1)
	presentFlag    = byte(0)
)

var (
	// ErrInvalidSegment indicates that a segment cannot be decoded safely.
	ErrInvalidSegment = errors.New("invalid segment")
	// ErrChecksumMismatch indicates that segment contents changed after publication.
	ErrChecksumMismatch = errors.New("segment checksum mismatch")
	// ErrUnsupportedVersion indicates that a segment uses an unknown file format.
	ErrUnsupportedVersion = errors.New("unsupported segment version")
)

// Mutation describes a document replacement or deletion.
type Mutation struct {
	Document search.Document
	Deleted  bool
}

// Upsert creates a document replacement mutation.
func Upsert(document search.Document) Mutation {
	document.ID = strings.TrimSpace(document.ID)
	return Mutation{Document: document}
}

// Delete creates a document deletion mutation.
func Delete(id string) Mutation {
	return Mutation{Document: search.Document{ID: strings.TrimSpace(id)}, Deleted: true}
}

type record struct {
	document search.Document
	deleted  bool
}

type termPosting struct {
	documentOrdinal uint64
	titleFrequency  uint64
	bodyFrequency   uint64
}

type fileSegment struct {
	generation uint64
	records    []record
	postings   map[string][]termPosting
}

func writeSegment(output io.Writer, generation uint64, records []record) error {
	if generation == 0 {
		return fmt.Errorf("%w: generation must be positive", ErrInvalidSegment)
	}

	payload, err := encodePayload(records)
	if err != nil {
		return err
	}
	checksum := sha256.Sum256(payload)

	header := make([]byte, headerSize)
	copy(header[:8], fileMagic)
	binary.BigEndian.PutUint16(header[8:10], formatVersion)
	binary.BigEndian.PutUint16(header[10:12], headerSize)
	binary.BigEndian.PutUint64(header[12:20], generation)
	binary.BigEndian.PutUint64(header[20:28], uint64(len(payload)))
	copy(header[28:], checksum[:])

	if _, err := output.Write(header); err != nil {
		return fmt.Errorf("write segment header: %w", err)
	}
	if _, err := output.Write(payload); err != nil {
		return fmt.Errorf("write segment payload: %w", err)
	}
	return nil
}

func readSegment(input io.Reader) (fileSegment, error) {
	header := make([]byte, headerSize)
	if _, err := io.ReadFull(input, header); err != nil {
		return fileSegment{}, fmt.Errorf("%w: read header: %w", ErrInvalidSegment, err)
	}
	if string(header[:8]) != fileMagic {
		return fileSegment{}, fmt.Errorf("%w: incorrect magic", ErrInvalidSegment)
	}
	version := binary.BigEndian.Uint16(header[8:10])
	if version != formatVersion {
		return fileSegment{}, fmt.Errorf("%w: %d", ErrUnsupportedVersion, version)
	}
	if size := binary.BigEndian.Uint16(header[10:12]); size != headerSize {
		return fileSegment{}, fmt.Errorf("%w: header size %d", ErrInvalidSegment, size)
	}
	generation := binary.BigEndian.Uint64(header[12:20])
	if generation == 0 {
		return fileSegment{}, fmt.Errorf("%w: zero generation", ErrInvalidSegment)
	}
	payloadSize := binary.BigEndian.Uint64(header[20:28])
	if payloadSize > maxPayloadSize {
		return fileSegment{}, fmt.Errorf("%w: payload is too large", ErrInvalidSegment)
	}

	payload := make([]byte, payloadSize)
	if _, err := io.ReadFull(input, payload); err != nil {
		return fileSegment{}, fmt.Errorf("%w: read payload: %w", ErrInvalidSegment, err)
	}
	var trailing [1]byte
	if count, err := input.Read(trailing[:]); count != 0 || !errors.Is(err, io.EOF) {
		return fileSegment{}, fmt.Errorf("%w: trailing data", ErrInvalidSegment)
	}

	expectedChecksum := header[28:60]
	actualChecksum := sha256.Sum256(payload)
	if !bytes.Equal(actualChecksum[:], expectedChecksum) {
		return fileSegment{}, ErrChecksumMismatch
	}

	segment, err := decodePayload(payload)
	if err != nil {
		return fileSegment{}, err
	}
	segment.generation = generation
	return segment, nil
}

func encodePayload(records []record) ([]byte, error) {
	records = slices.Clone(records)
	slices.SortFunc(records, func(left, right record) int {
		return strings.Compare(left.document.ID, right.document.ID)
	})
	for position, current := range records {
		if strings.TrimSpace(current.document.ID) == "" {
			return nil, fmt.Errorf("%w: document ID is required", ErrInvalidSegment)
		}
		if position > 0 && records[position-1].document.ID == current.document.ID {
			return nil, fmt.Errorf("%w: duplicate document ID %q", ErrInvalidSegment, current.document.ID)
		}
	}

	postings := buildPostings(records)
	var payload bytes.Buffer
	writeUvarint(&payload, uint64(len(records)))
	for _, current := range records {
		if current.deleted {
			payload.WriteByte(deleteFlag)
		} else {
			payload.WriteByte(presentFlag)
		}
		writeString(&payload, current.document.ID)
		if !current.deleted {
			writeString(&payload, current.document.Title)
			writeString(&payload, current.document.Body)
		}
	}

	terms := make([]string, 0, len(postings))
	for term := range postings {
		terms = append(terms, term)
	}
	slices.Sort(terms)
	writeUvarint(&payload, uint64(len(terms)))
	for _, term := range terms {
		writeString(&payload, term)
		writeUvarint(&payload, uint64(len(postings[term])))
		var previousOrdinal uint64
		for position, posting := range postings[term] {
			delta := posting.documentOrdinal
			if position > 0 {
				delta -= previousOrdinal
			}
			writeUvarint(&payload, delta)
			writeUvarint(&payload, posting.titleFrequency)
			writeUvarint(&payload, posting.bodyFrequency)
			previousOrdinal = posting.documentOrdinal
		}
	}
	if payload.Len() > maxPayloadSize {
		return nil, fmt.Errorf("%w: payload is too large", ErrInvalidSegment)
	}
	return payload.Bytes(), nil
}

func decodePayload(payload []byte) (fileSegment, error) {
	decoder := payloadDecoder{reader: bytes.NewReader(payload)}
	recordCount, err := decoder.readCount(maxRecordCount, "record")
	if err != nil {
		return fileSegment{}, err
	}
	records := make([]record, 0, recordCount)
	for position := uint64(0); position < recordCount; position++ {
		flag, err := decoder.reader.ReadByte()
		if err != nil {
			return fileSegment{}, decoder.invalid("read record flag", err)
		}
		if flag != presentFlag && flag != deleteFlag {
			return fileSegment{}, decoder.invalid("unknown record flag", nil)
		}
		id, err := decoder.readString()
		if err != nil {
			return fileSegment{}, err
		}
		if strings.TrimSpace(id) == "" {
			return fileSegment{}, decoder.invalid("empty document ID", nil)
		}
		if len(records) > 0 && records[len(records)-1].document.ID >= id {
			return fileSegment{}, decoder.invalid("document IDs are not ordered", nil)
		}

		current := record{document: search.Document{ID: id}, deleted: flag == deleteFlag}
		if !current.deleted {
			current.document.Title, err = decoder.readString()
			if err != nil {
				return fileSegment{}, err
			}
			current.document.Body, err = decoder.readString()
			if err != nil {
				return fileSegment{}, err
			}
		}
		records = append(records, current)
	}

	termCount, err := decoder.readCount(maxTermCount, "term")
	if err != nil {
		return fileSegment{}, err
	}
	postings := make(map[string][]termPosting, termCount)
	var previousTerm string
	for termPosition := uint64(0); termPosition < termCount; termPosition++ {
		term, err := decoder.readString()
		if err != nil {
			return fileSegment{}, err
		}
		if term == "" || (termPosition > 0 && previousTerm >= term) {
			return fileSegment{}, decoder.invalid("terms are not ordered", nil)
		}
		postingCount, err := decoder.readCount(recordCount, "posting")
		if err != nil {
			return fileSegment{}, err
		}
		termPostings := make([]termPosting, 0, postingCount)
		var previousOrdinal uint64
		for postingPosition := uint64(0); postingPosition < postingCount; postingPosition++ {
			delta, err := decoder.readUvarint()
			if err != nil {
				return fileSegment{}, err
			}
			ordinal := delta
			if postingPosition > 0 {
				if delta > ^uint64(0)-previousOrdinal {
					return fileSegment{}, decoder.invalid("posting ordinal overflow", nil)
				}
				ordinal = previousOrdinal + delta
				if ordinal <= previousOrdinal {
					return fileSegment{}, decoder.invalid("posting ordinals are not ordered", nil)
				}
			}
			if ordinal >= uint64(len(records)) || records[ordinal].deleted {
				return fileSegment{}, decoder.invalid("posting references an invalid record", nil)
			}
			titleFrequency, err := decoder.readUvarint()
			if err != nil {
				return fileSegment{}, err
			}
			bodyFrequency, err := decoder.readUvarint()
			if err != nil {
				return fileSegment{}, err
			}
			if titleFrequency == 0 && bodyFrequency == 0 {
				return fileSegment{}, decoder.invalid("empty posting", nil)
			}
			termPostings = append(termPostings, termPosting{
				documentOrdinal: ordinal,
				titleFrequency:  titleFrequency,
				bodyFrequency:   bodyFrequency,
			})
			previousOrdinal = ordinal
		}
		postings[term] = termPostings
		previousTerm = term
	}

	if decoder.reader.Len() != 0 {
		return fileSegment{}, decoder.invalid("trailing payload data", nil)
	}
	return fileSegment{records: records, postings: postings}, nil
}

func buildPostings(records []record) map[string][]termPosting {
	postings := make(map[string][]termPosting)
	var documentOrdinal uint64
	for _, current := range records {
		if current.deleted {
			documentOrdinal++
			continue
		}
		titleTerms := frequencies(search.Tokenize(current.document.Title))
		bodyTerms := frequencies(search.Tokenize(current.document.Body))
		terms := make(map[string]struct{}, len(titleTerms)+len(bodyTerms))
		for term := range titleTerms {
			terms[term] = struct{}{}
		}
		for term := range bodyTerms {
			terms[term] = struct{}{}
		}
		for term := range terms {
			postings[term] = append(postings[term], termPosting{
				documentOrdinal: documentOrdinal,
				titleFrequency:  titleTerms[term],
				bodyFrequency:   bodyTerms[term],
			})
		}
		documentOrdinal++
	}
	return postings
}

func frequencies(terms []string) map[string]uint64 {
	result := make(map[string]uint64, len(terms))
	for _, term := range terms {
		result[term]++
	}
	return result
}

func writeString(output *bytes.Buffer, value string) {
	writeUvarint(output, uint64(len(value)))
	output.WriteString(value)
}

func writeUvarint(output *bytes.Buffer, value uint64) {
	var encoded [binary.MaxVarintLen64]byte
	length := binary.PutUvarint(encoded[:], value)
	output.Write(encoded[:length])
}

type payloadDecoder struct {
	reader *bytes.Reader
}

func (d payloadDecoder) readCount(maximum uint64, label string) (uint64, error) {
	value, err := d.readUvarint()
	if err != nil {
		return 0, err
	}
	if value > maximum {
		return 0, d.invalid(label+" count exceeds limit", nil)
	}
	return value, nil
}

func (d payloadDecoder) readString() (string, error) {
	length, err := d.readUvarint()
	if err != nil {
		return "", err
	}
	if length > maxStringSize {
		return "", d.invalid("string length exceeds limit", nil)
	}
	value := make([]byte, length)
	if _, err := io.ReadFull(d.reader, value); err != nil {
		return "", d.invalid("read string", err)
	}
	return string(value), nil
}

func (d payloadDecoder) readUvarint() (uint64, error) {
	value, err := binary.ReadUvarint(d.reader)
	if err != nil {
		return 0, d.invalid("read integer", err)
	}
	return value, nil
}

func (payloadDecoder) invalid(message string, cause error) error {
	if cause == nil {
		return fmt.Errorf("%w: %s", ErrInvalidSegment, message)
	}
	return fmt.Errorf("%w: %s: %w", ErrInvalidSegment, message, cause)
}
