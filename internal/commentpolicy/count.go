package commentpolicy

import (
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"
)

const MaxWords = 250

var ErrInvalidUTF8 = errors.New("bot comment contains invalid UTF-8")

type WordLimitError struct{ Count int }

func (e *WordLimitError) Error() string {
	return fmt.Sprintf("Bot comments are limited to %d words (received %d). Shorten and retry.", MaxWords, e.Count)
}

func separator(r rune) bool {
	return r >= 0x09 && r <= 0x0d || r == 0x20 || r == 0x85 || r == 0xa0 || r == 0x1680 ||
		r >= 0x2000 && r <= 0x200a || r == 0x2028 || r == 0x2029 || r == 0x202f || r == 0x205f || r == 0x3000
}

// WebStoredBody mirrors Web's outer Python strip before raw-body counting.
// Python also strips U+001C–U+001F at the edges; these are not word separators.
func WebStoredBody(body string) string {
	return strings.TrimFunc(body, func(r rune) bool { return separator(r) || r >= 0x1c && r <= 0x1f })
}

func CountWords(body string) (int, error) {
	if !utf8.ValidString(body) {
		return 0, ErrInvalidUTF8
	}
	count := 0
	inWord := false
	for _, r := range body {
		if separator(r) {
			inWord = false
			continue
		}
		if !inWord {
			count++
			inWord = true
		}
	}
	return count, nil
}

func ValidateBotComment(body string) error {
	count, err := CountWords(body)
	if err != nil {
		return err
	}
	if count > MaxWords {
		return &WordLimitError{Count: count}
	}
	return nil
}
