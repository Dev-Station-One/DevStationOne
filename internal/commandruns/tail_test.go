package commandruns

import (
	"strings"
	"testing"
)

func TestTail(t *testing.T) {
	// ANSI-stripping + \r removal.
	if got := Tail([]byte("\x1b[32mgreen\x1b[0m\r\nline2\r")); strings.Contains(got, "\x1b") || strings.Contains(got, "\r") {
		t.Fatalf("tail = %q, want ANSI/\\r stripped", got)
	}
	if got := Tail([]byte("\x1b[32mgreen\x1b[0m\nline2")); !strings.Contains(got, "green") || !strings.Contains(got, "line2") {
		t.Fatalf("tail = %q, want visible lines", got)
	}
	// Invalid UTF-8 becomes valid replacement runes (the text column and
	// JSON require it).
	bad := []byte{'a', 0xff, 'b'}
	if got := Tail(bad); !strings.Contains(got, "�") {
		t.Fatalf("tail = %q, want replacement char", got)
	}
	// Only the last tailLimit bytes are kept.
	var big []byte
	for i := 0; i < tailLimit/10+50; i++ {
		big = append(big, 'x')
	}
	if got := Tail(big); len(got) > tailLimit {
		t.Fatalf("tail = %d bytes, want at most %d", len(got), tailLimit)
	}
	if got := Tail(nil); got != "" {
		t.Fatalf("tail(nil) = %q, want empty", got)
	}
}
