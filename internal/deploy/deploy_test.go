package deploy

import (
	"context"
	"testing"
	"time"

	"interzone/internal/config"
)

func TestFailureMessage(t *testing.T) {
	line := `fatal: [matrix.example.org -> 127.0.0.1]: FAILED! => {"changed": false, "msg": "Failed checking Element Web is up at ` + "`element.example.org`" + `. Full error: {'status': -1}"}`
	got := FailureMessage("Fail if Element Web not working", line)
	want := "Failed checking Element Web is up at `element.example.org`."
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
	if got := FailureMessage("Fail if Element Web not working", "fatal: unreachable"); got != "Element Web not working" {
		t.Fatalf("fallback: got %q", got)
	}
}

func TestRedact(t *testing.T) {
	got := redact([]string{"--extra-vars", `{"password":"x","username":"a"}`, "ansible_become_password=y", "plain"})
	want := []string{"--extra-vars", "'<hidden>'", "ansible_become_password=••••", "plain"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("arg %d: got %q, want %q", i, got[i], want[i])
		}
	}
}

func TestWithin(t *testing.T) {
	for name, ok := range map[string]bool{
		"interzone-bundle/playbook/setup.yml": true,
		"../escape":                           false,
		"a/../../escape":                      false,
		"/etc/passwd":                         true, // joined under root, not absolute
	} {
		if _, got := within("/tmp/root", name); got != ok {
			t.Errorf("within(%q) = %v, want %v", name, got, ok)
		}
	}
}

func TestExecLongLine(t *testing.T) {
	r := &Runner{cfg: config.Default(), events: make(chan Event, 1024)}
	go func() {
		for range r.events {
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	// A 3 MB line, then one more: neither may block the command.
	err := r.exec(ctx, StepPlaybook, t.TempDir(), "sh", "-c", "head -c 3000000 /dev/zero | tr '\\0' x; echo; echo done")
	if err != nil {
		t.Fatal(err)
	}
	if ctx.Err() != nil {
		t.Fatal("exec hung on a long line")
	}
}
