package cli

import (
	"flag"
	"slices"
	"testing"
)

func TestParseInterspersed(t *testing.T) {
	for _, tc := range []struct {
		args []string
		yes  bool
		pos  []string
	}{
		{[]string{"--yes", "dump.sql"}, true, []string{"dump.sql"}},
		{[]string{"dump.sql", "--yes"}, true, []string{"dump.sql"}},
		{[]string{"a", "-yes", "b"}, true, []string{"a", "b"}},
		{[]string{"dump.sql"}, false, []string{"dump.sql"}},
		{[]string{"--", "-yes"}, false, []string{"-yes"}},
		{[]string{"a", "--", "-yes"}, false, []string{"a", "-yes"}},
		{nil, false, nil},
	} {
		fs := flag.NewFlagSet("t", flag.ContinueOnError)
		yes := fs.Bool("yes", false, "")
		parse(fs, tc.args)
		if *yes != tc.yes || !slices.Equal(fs.Args(), tc.pos) {
			t.Errorf("parse(%q): yes=%v args=%q, want %v %q", tc.args, *yes, fs.Args(), tc.yes, tc.pos)
		}
	}
}
