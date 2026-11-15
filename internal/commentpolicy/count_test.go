package commentpolicy

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCountFixtures(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "testdata", "bot_comment_word_count.json"))
	if err != nil {
		t.Fatal(err)
	}
	var cases struct {
		Fixtures []struct {
			ID    string `json:"id"`
			Body  string `json:"body"`
			Words int    `json:"words"`
		} `json:"fixtures"`
	}
	if err := json.Unmarshal(data, &cases); err != nil {
		t.Fatal(err)
	}
	for _, tc := range cases.Fixtures {
		t.Run(tc.ID, func(t *testing.T) {
			got, err := CountWords(tc.Body)
			if err != nil || got != tc.Words {
				t.Fatalf("count = %d, %v; want %d", got, err, tc.Words)
			}
			valid := ValidateBotComment(tc.Body)
			if (valid == nil) != (tc.Words <= 250) {
				t.Fatalf("validation = %v for %d words", valid, tc.Words)
			}
		})
	}
}

func TestWebCasesAndInvalidUTF8(t *testing.T) {
	for _, tc := range []struct {
		body string
		want int
	}{
		{"alpha\u00a0beta\u3000gamma\r\ndelta", 4},
		{"**bold** [link](https://example.com/a?b=c) @Paul -", 4},
		{"alpha\u200bbeta", 1}, {"你好世界", 1}, {"go @Paul", 2},
		{strings.Join([]string{"a", "b", "c"}, " "), 3},
		{"\x1ca\x1f", 1},
	} {
		got, err := CountWords(tc.body)
		if err != nil || got != tc.want {
			t.Fatalf("%q: %d, %v", tc.body, got, err)
		}
	}
	if _, err := CountWords(string([]byte{0xff})); err == nil {
		t.Fatal("accepted invalid UTF-8")
	}
	if got := WebStoredBody("\x1c alpha \x1f"); got != "alpha" {
		t.Fatalf("Web outer trim = %q", got)
	}
	if got := WebStoredBody("alpha\x1cbeta"); got != "alpha\x1cbeta" {
		t.Fatalf("inner control changed: %q", got)
	}
}
