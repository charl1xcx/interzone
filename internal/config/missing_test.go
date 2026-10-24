package config

import (
	"strings"
	"testing"
)

func TestMissingFlagsInvalidExtraYAML(t *testing.T) {
	c := Default()
	c.Domain = "example.org"
	c.ExtraYAML = "sss\ns\ns"
	got := strings.Join(c.Missing(), "\n")
	if !strings.Contains(got, "aren't valid YAML") {
		t.Fatalf("invalid extra YAML not reported: %q", got)
	}
	c.ExtraYAML = "matrix_synapse_max_upload_size_mb: 200\n"
	if m := c.Missing(); len(m) != 0 {
		t.Fatalf("valid extra YAML reported: %v", m)
	}
}
