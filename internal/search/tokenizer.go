package search

import (
	"strings"
	"unicode"

	"golang.org/x/text/unicode/norm"
)

// Tokenize normalizes text and splits it into searchable terms.
func Tokenize(text string) []string {
	return tokenize(text)
}

func tokenize(text string) []string {
	normalized := strings.ToLower(norm.NFKC.String(text))
	return strings.FieldsFunc(normalized, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsNumber(r) && !unicode.IsMark(r)
	})
}
