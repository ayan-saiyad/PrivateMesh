package search

import (
	"reflect"
	"testing"
)

func TestTokenizeNormalizesUnicode(t *testing.T) {
	t.Parallel()

	got := tokenize("CAFÉ cafe\u0301 ＣＡＦＥ हिंदी १२३")
	want := []string{"café", "café", "cafe", "हिंदी", "१२३"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("tokenize() = %q, want %q", got, want)
	}
}

func TestTokenizeUsesPunctuationAsBoundaries(t *testing.T) {
	t.Parallel()

	got := tokenize("local-first/search_node")
	want := []string{"local", "first", "search", "node"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("tokenize() = %q, want %q", got, want)
	}
}
