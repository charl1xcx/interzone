package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRenameOld(t *testing.T) {
	pb := t.TempDir()
	dir := filepath.Join(pb, "roles", "custom", "matrix-livekit-jwt-service", "tasks")
	os.MkdirAll(dir, 0o755)
	os.WriteFile(filepath.Join(dir, "validate_config.yml"), []byte(`
  with_items:
    - {'old': 'matrix_livekit_jwt_service_container_additional_arguments', 'new': 'matrix_livekit_jwt_service_container_extra_arguments'}
    - {'old': 'matrix_livekit_jwt_service_container_image_force_pull', 'new': '<removed> (handled natively)'}
    - { "old": "a_one", "new": "a_two" }
    - { "old": "a_two", "new": "a_three" }
`), 0o644)

	c := Default()
	c.Overrides["a_one"] = "1"
	c.Values["a_two"] = "x"
	c.Values["a_three"] = "kept"
	c.ExtraYAML = `# keep me
matrix_livekit_jwt_service_container_additional_arguments:
  - --foo
matrix_livekit_jwt_service_container_image_force_pull: true
a_two: old
a_three: new`
	if got := len(c.RenameOld(pb)); got != 4 {
		t.Errorf("%d renames, want 4", got)
	}
	if c.Overrides["a_three"] != "1" || len(c.Overrides) != 1 {
		t.Errorf("overrides %v", c.Overrides)
	}
	if c.Values["a_three"] != "kept" || c.Values["a_two"] != "" {
		t.Errorf("values %v", c.Values)
	}
	want := `# keep me
matrix_livekit_jwt_service_container_extra_arguments:
  - --foo
matrix_livekit_jwt_service_container_image_force_pull: true
# a_two: old
a_three: new`
	if c.ExtraYAML != want {
		t.Errorf("extra vars:\n%s\nwant:\n%s", c.ExtraYAML, want)
	}
}
