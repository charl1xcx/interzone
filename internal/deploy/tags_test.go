package deploy

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestServiceGroups(t *testing.T) {
	pb := t.TempDir()
	os.MkdirAll(filepath.Join(pb, "group_vars"), 0o755)
	os.WriteFile(filepath.Join(pb, "group_vars", "matrix_servers"), []byte(`
      'groups': ['matrix', 'postgres'],
      'groups': ['matrix', 'bridges', 'mautrix-slack'],
      'groups': ['matrix', 'homeservers', matrix_homeserver_implementation],
`), 0o644)
	if got, want := ServiceGroups(pb), []string{"bridges", "homeservers", "mautrix-slack", "postgres"}; !slices.Equal(got, want) {
		t.Errorf("ServiceGroups = %v, want %v", got, want)
	}
}
