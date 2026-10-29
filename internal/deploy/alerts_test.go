package deploy

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"interzone/internal/config"
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
	stub("curl", `for a; do [ "$a" = '%{http_code}' ] && { [ -f "$IZ_DIR/failing" ] && echo 000 || echo 401; exit 0; }; done
for a; do [ "$a" = --resolve ] && { [ -f "$IZ_DIR/failing" ] && exit 22; exit 0; }; done
for a; do [ "$a" = @- ] && { cat >> "$IZ_DIR/sent"; echo >> "$IZ_DIR/sent"; exit 0; }; done
printf '%s\n' "${@: -2:1}" >> "$IZ_DIR/sent"`)
	env := filepath.Join(dir, "env")
	_ = os.WriteFile(env, []byte("IZ_DOMAIN=example.org\nIZ_HOST=matrix.example.org\nIZ_SCHEME=http\nIZ_PORT=80\nIZ_URL=https://ntfy.example/t\nIZ_JSON=false\nIZ_MIRRORS=https://reg.example/v2/\n"), 0o600)
	script := filepath.Join(dir, "interzone-alerts")
	_ = os.WriteFile(script, []byte(alertsScript), 0o755)

	run := func(args ...string) string {
		cmd := exec.Command("bash", append([]string{script}, args...)...)
		cmd.Env = append(os.Environ(), "PATH="+filepath.Join(dir, "bin")+":"+os.Getenv("PATH"), "IZ_DIR="+dir,
			"IZ_ENV="+env, "IZ_STATE="+filepath.Join(dir, "state"), "IZ_NEWS="+filepath.Join(dir, "news"))
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
	if !strings.Contains(got, "matrix-synapse.service has failed") || !strings.Contains(got, "http://matrix.example.org doesn't answer") ||
		!strings.Contains(got, "the mirror https://reg.example/v2/ doesn't answer") {
		t.Errorf("problems sent as %q", got)
	}
	if got := run(); got != "" {
		t.Errorf("the same problems were sent again: %q", got)
	}
	_ = os.Remove(filepath.Join(dir, "failing"))
	if got := run(); !strings.Contains(got, "fine again") {
		t.Errorf("recovery sent as %q", got)
	}

	// A day after the last look, new changelog entries are sent, this
	// server's first, and only once.
	stub("git", `[ "$1" = clone ] && exit 0
printf '# 2026-10-28\n\n## Something about Jitsi\n\nOther.\n\n## Bridges: see [docs](docs/configuring-playbook-bridge-mautrix-telegram.md)\n\n## Old one\n'`)
	news := filepath.Join(dir, "news")
	_ = os.WriteFile(news, []byte("Old one\n"), 0o644)
	_ = os.Chtimes(news, time.Now().Add(-25*time.Hour), time.Now().Add(-25*time.Hour))
	_ = os.WriteFile(env, []byte("IZ_DOMAIN=example.org\nIZ_HOST=matrix.example.org\nIZ_SCHEME=http\nIZ_PORT=80\nIZ_URL=https://ntfy.example/t\nIZ_JSON=false\n"+
		"IZ_PLAYBOOK=https://git.example/mdad\nIZ_HOMESERVER=Synapse\nIZ_WATCH=configuring-playbook-bridge-mautrix-telegram.md\n"), 0o600)
	got = run()
	if !strings.Contains(got, "2 new") || !strings.Contains(got, "★ Bridges: see docs\n• Something about Jitsi") {
		t.Errorf("news sent as %q", got)
	}
	if got := run(); got != "" {
		t.Errorf("news sent again: %q", got)
	}
	_ = os.WriteFile(env, []byte("IZ_DOMAIN=example.org\nIZ_HOST=matrix.example.org\nIZ_SCHEME=http\nIZ_PORT=80\nIZ_URL=https://hook.example/x\nIZ_JSON=true\n"), 0o600)
	if got := run("test"); !strings.Contains(got, `"text": "interzone alerts are on for example.org`) {
		t.Errorf("JSON test message sent as %q", got)
	}

	// To a Matrix room: joined by its alias, then a formatted message.
	var paths []string
	var msg map[string]string
	hs := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.Method+" "+r.URL.EscapedPath()+" "+r.Header.Get("Authorization"))
		if r.Method == "PUT" {
			b, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(b, &msg)
		}
		_, _ = io.WriteString(w, `{"room_id": "!abc:example.org", "event_id": "$e"}`)
	}))
	defer hs.Close()
	_ = os.WriteFile(env, []byte("IZ_DOMAIN=example.org\nIZ_HOST=matrix.example.org\nIZ_SCHEME=http\nIZ_PORT=80\nIZ_URL=\nIZ_JSON=false\n"+
		"IZ_MATRIX=true\nIZ_MATRIX_HS="+hs.URL+"\nIZ_MATRIX_ROOM='#alerts:example.org'\nIZ_MATRIX_TOKEN=sekrit\n"), 0o600)
	t.Setenv("no_proxy", "127.0.0.1")
	_ = os.WriteFile(filepath.Join(dir, "failing"), nil, 0o644)
	run()
	if len(paths) != 2 || paths[0] != "POST /_matrix/client/v3/join/%23alerts%3Aexample.org Bearer sekrit" ||
		!strings.HasPrefix(paths[1], "PUT /_matrix/client/v3/rooms/%21abc%3Aexample.org/send/m.room.message/interzone") {
		t.Fatalf("Matrix requests: %q", paths)
	}
	f := msg["formatted_body"]
	if msg["format"] != "org.matrix.custom.html" || !strings.HasPrefix(f, `<h4><font data-mx-color="#e5484d"`) ||
		!strings.Contains(f, "<li>matrix-synapse.service has failed</li>") || !strings.Contains(f, "doesn&#x27;t answer") ||
		!strings.Contains(msg["body"], "• matrix-synapse.service has failed") {
		t.Errorf("Matrix message: %q", msg)
	}
}

func TestAlertsProblem(t *testing.T) {
	ok := config.Alerts{Matrix: true, Homeserver: "https://matrix.example.org", Room: "#alerts:example.org", Token: "t"}
	if p := AlertsProblem(ok); p != "" {
		t.Error(p)
	}
	for _, bad := range []config.Alerts{{URL: "ntfy.sh/x"}, {Matrix: true, Homeserver: "matrix.example.org", Room: "#a:b", Token: "t"},
		{Matrix: true, Homeserver: "https://m.example", Room: "alerts", Token: "t"}, {Matrix: true, Homeserver: "https://m.example", Room: "!a:b"}} {
		if AlertsProblem(bad) == "" {
			t.Errorf("%+v passed", bad)
		}
	}
}
