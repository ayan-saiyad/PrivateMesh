package search

import (
	"strings"
	"unicode"

	"golang.org/x/text/unicode/norm"
)

func tokenize(text string) []string {
	normalized := strings.ToLower(norm.NFKC.String(text))
	return strings.FieldsFunc(normalized, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsNumber(r) && !unicode.IsMark(r)
	})
}
