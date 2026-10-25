package ui

import (
	"strings"
	"testing"
)

func TestLineDiff(t *testing.T) {
	d := lineDiff(strings.Split("a\nb\nc\nd", "\n"), strings.Split("a\nx\nc\nd\ne", "\n"))
	var got []string
	for _, l := range d {
		got = append(got, string(l.op)+l.text)
	}
	if want := " a,-b,+x, c, d,+e"; strings.Join(got, ",") != want {
		t.Fatalf("got %s, want %s", strings.Join(got, ","), want)
	}
	if a, r := diffChanges(d); a != 2 || r != 1 {
		t.Fatalf("changes %d+ %d-", a, r)
	}
}
