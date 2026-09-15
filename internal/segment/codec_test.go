package segment

import (
	"bytes"
	"encoding/binary"
	"errors"
	"reflect"
	"testing"

	"github.com/ayansaiyad/privatemesh/internal/search"
)

func TestSegmentRoundTrip(t *testing.T) {
	t.Parallel()

	records := []record{
		{document: search.Document{ID: "doc-b", Title: "Local search", Body: "mesh mesh node"}},
		{document: search.Document{ID: "doc-c"}, deleted: true},
		{document: search.Document{ID: "doc-a", Title: "Mesh search", Body: "local"}},
	}
	var encoded bytes.Buffer
	if err := writeSegment(&encoded, 42, records); err != nil {
		t.Fatal(err)
	}

	decoded, err := readSegment(bytes.NewReader(encoded.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	if decoded.generation != 42 {
		t.Fatalf("generation = %d, want 42", decoded.generation)
	}
	if got := recordIDs(decoded.records); !reflect.DeepEqual(got, []string{"doc-a", "doc-b", "doc-c"}) {
		t.Fatalf("record IDs = %v", got)
	}
	mesh := decoded.postings["mesh"]
	if len(mesh) != 2 || mesh[0].documentOrdinal != 0 || mesh[1].documentOrdinal != 1 {
		t.Fatalf("mesh postings = %+v", mesh)
	}
	if mesh[1].titleFrequency != 0 || mesh[1].bodyFrequency != 2 {
		t.Fatalf("doc-b mesh frequencies = %+v", mesh[1])
	}
}

func TestSegmentRejectsChecksumMismatch(t *testing.T) {
	t.Parallel()

	var encoded bytes.Buffer
	if err := writeSegment(&encoded, 1, []record{{
		document: search.Document{ID: "doc-1", Body: "searchable"},
	}}); err != nil {
		t.Fatal(err)
	}
	contents := encoded.Bytes()
	contents[len(contents)-1] ^= 0xff

	if _, err := readSegment(bytes.NewReader(contents)); !errors.Is(err, ErrChecksumMismatch) {
		t.Fatalf("readSegment() error = %v, want %v", err, ErrChecksumMismatch)
	}
}

func TestSegmentRejectsUnsupportedVersion(t *testing.T) {
	t.Parallel()

	var encoded bytes.Buffer
	if err := writeSegment(&encoded, 1, nil); err != nil {
		t.Fatal(err)
	}
	contents := encoded.Bytes()
	binary.BigEndian.PutUint16(contents[8:10], formatVersion+1)

	if _, err := readSegment(bytes.NewReader(contents)); !errors.Is(err, ErrUnsupportedVersion) {
		t.Fatalf("readSegment() error = %v, want %v", err, ErrUnsupportedVersion)
	}
}

func TestSegmentRejectsDuplicateDocumentIDs(t *testing.T) {
	t.Parallel()

	records := []record{
		{document: search.Document{ID: "duplicate"}},
		{document: search.Document{ID: "duplicate"}, deleted: true},
	}
	if err := writeSegment(&bytes.Buffer{}, 1, records); !errors.Is(err, ErrInvalidSegment) {
		t.Fatalf("writeSegment() error = %v, want %v", err, ErrInvalidSegment)
	}
}

func TestSegmentRejectsTruncation(t *testing.T) {
	t.Parallel()

	var encoded bytes.Buffer
	if err := writeSegment(&encoded, 1, []record{{
		document: search.Document{ID: "doc-1", Body: "searchable"},
	}}); err != nil {
		t.Fatal(err)
	}
	contents := encoded.Bytes()

	if _, err := readSegment(bytes.NewReader(contents[:len(contents)-1])); !errors.Is(err, ErrInvalidSegment) {
		t.Fatalf("readSegment() error = %v, want %v", err, ErrInvalidSegment)
	}
}

func recordIDs(records []record) []string {
	ids := make([]string, len(records))
	for position, current := range records {
		ids[position] = current.document.ID
	}
	return ids
}
