package deploy

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The alert script sends once when problems appear, nothing while they
// stay the same, and once when they're gone.
func TestAlertsScript(t *testing.T) {
	if strings.Contains(alertsScript, "{{") || strings.Contains(alertsScript, "{%") || strings.Contains(alertsScript, "{#") {
		t.Fatal("the script has Jinja markers Ansible would template")
	}
	for _, bin := range []string{"bash", "python3"} {
		if _, err := exec.LookPath(bin); err != nil {
			t.Skip(bin + " isn't installed")
		}
	}
	dir := t.TempDir()
	stub := func(name, body string) {
		if err := os.WriteFile(filepath.Join(dir, "bin", name), []byte("#!/bin/bash\n"+body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	_ = os.MkdirAll(filepath.Join(dir, "bin"), 0o755)
	sent := filepath.Join(dir, "sent")
	stub("systemctl", `[ -f "$IZ_DIR/failing" ] && echo "matrix-synapse.service loaded failed failed Synapse"; true`)
	stub("df", `echo "Filesystem 1024-blocks Used Available Capacity Mounted"; echo "/dev/sda 100 50 50 50% /"`)
	stub("curl", `for a; do [ "$a" = --resolve ] && { [ -f "$IZ_DIR/failing" ] && exit 22; exit 0; }; done
for a; do [ "$a" = @- ] && { cat >> "$IZ_DIR/sent"; echo >> "$IZ_DIR/sent"; exit 0; }; done
printf '%s\n' "${@: -2:1}" >> "$IZ_DIR/sent"`)
	env := filepath.Join(dir, "env")
	_ = os.WriteFile(env, []byte("IZ_DOMAIN=example.org\nIZ_HOST=matrix.example.org\nIZ_SCHEME=http\nIZ_PORT=80\nIZ_URL=https://ntfy.example/t\nIZ_JSON=false\n"), 0o600)
	script := filepath.Join(dir, "interzone-alerts")
	_ = os.WriteFile(script, []byte(alertsScript), 0o755)

	run := func(args ...string) string {
		cmd := exec.Command("bash", append([]string{script}, args...)...)
		cmd.Env = append(os.Environ(), "PATH="+filepath.Join(dir, "bin")+":"+os.Getenv("PATH"), "IZ_DIR="+dir,
			"IZ_ENV="+env, "IZ_STATE="+filepath.Join(dir, "state"))
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%v: %s", err, out)
		}
		b, _ := os.ReadFile(sent)
		_ = os.Remove(sent)
		return string(b)
	}
	if got := run(); got != "" {
		t.Errorf("all fine at first, yet sent %q", got)
	}
	_ = os.WriteFile(filepath.Join(dir, "failing"), nil, 0o644)
	got := run()
	if !strings.Contains(got, "matrix-synapse.service has failed") || !strings.Contains(got, "http://matrix.example.org doesn't answer") {
		t.Errorf("problems sent as %q", got)
	}
	if got := run(); got != "" {
		t.Errorf("the same problems were sent again: %q", got)
	}
	_ = os.Remove(filepath.Join(dir, "failing"))
	if got := run(); !strings.Contains(got, "fine again") {
		t.Errorf("recovery sent as %q", got)
	}
	_ = os.WriteFile(env, []byte("IZ_DOMAIN=example.org\nIZ_HOST=matrix.example.org\nIZ_SCHEME=http\nIZ_PORT=80\nIZ_URL=https://hook.example/x\nIZ_JSON=true\n"), 0o600)
	if got := run("test"); !strings.Contains(got, `"text": "interzone alerts are on for example.org`) {
		t.Errorf("JSON test message sent as %q", got)
	}
}
