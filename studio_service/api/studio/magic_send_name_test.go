package studio

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// A send's default name stays within the Timeline's 80 characters, however long the board's name (review, phase G).
func TestSendName(t *testing.T) {
	if got := sendName("Tide", 2); got != "Tide · send 2" {
		t.Fatal(got)
	}
	got := sendName(strings.Repeat("é", 80), 12)
	if utf8.RuneCountInString(got) != 80 || !strings.HasSuffix(got, " · send 12") {
		t.Fatalf("%q is %d characters", got, utf8.RuneCountInString(got))
	}
}
