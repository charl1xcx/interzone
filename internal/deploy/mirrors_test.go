package deploy

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"interzone/internal/config"
)

// TestKnownMirrors checks every mirror in the catalog over the network.
// It is skipped unless INTERZONE_MIRROR_TESTS=1, since it needs to reach
// servers in Iran:
//
//	INTERZONE_MIRROR_TESTS=1 go test ./internal/deploy -run KnownMirrors -v
func TestKnownMirrors(t *testing.T) {
	if os.Getenv("INTERZONE_MIRROR_TESTS") == "" {
		t.Skip("set INTERZONE_MIRROR_TESTS=1 to probe the known mirrors")
	}
	for _, m := range config.KnownMirrors {
		t.Run(string(m.Kind)+"/"+m.Name, func(t *testing.T) {
			t.Parallel()
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			detail, err := ProbeMirror(ctx, m.Kind, m.URL)
			if err != nil {
				t.Errorf("%s: %v", m.URL, err)
				return
			}
			t.Logf("%s: %s", m.URL, detail)
		})
	}
}

func TestDefaultMirrors(t *testing.T) {
	for _, k := range []config.MirrorKind{config.MirrorAPT, config.MirrorDocker, config.MirrorPyPI} {
		if config.DefaultMirror(k) == "" {
			t.Errorf("no default %s mirror", k)
		}
	}
}

func TestProbeMirror(t *testing.T) {
	fresh := "Date: " + time.Now().Add(-30*time.Hour).UTC().Format(time.RFC1123) + "\n"
	stale := "Date: " + time.Now().Add(-60*24*time.Hour).UTC().Format(time.RFC1123Z) + "\n"
	files := map[string]string{
		"/good/ubuntu/dists/noble-security/Release":           fresh,
		"/good/debian-security/dists/trixie-security/Release": fresh,
		"/old/ubuntu/dists/noble-security/Release":            fresh,
		"/old/debian-security/dists/trixie-security/Release":  stale,
		"/half/ubuntu/dists/noble-security/Release":           fresh,
		"/docker/ubuntu/dists/noble/Release":                  fresh,
		"/pypi/ansible-core/": `<a href="ansible_core-2.19.9.tar.gz">x</a>
<a href="ansible_core-2.19.14-py3-none-any.whl">x</a><a href="ansible-core-2.9.1.tar.gz">x</a>`,
		"/empty/ansible-core/": "<html></html>",
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, ok := files[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Write([]byte(body))
	}))
	defer srv.Close()

	for _, tc := range []struct {
		kind config.MirrorKind
		path string
		want string // substring of the detail, or of the error if wantErr
		err  bool
	}{
		{config.MirrorAPT, "/good", "Ubuntu 1 day old, Debian 1 day old", false},
		{config.MirrorAPT, "/old", "Debian security updates are 60 days old", true},
		{config.MirrorAPT, "/half", "Debian: HTTP 404", true},
		{config.MirrorDocker, "/docker/", "updated 1 day ago", false},
		{config.MirrorDocker, "/pypi", "HTTP 404", true},
		{config.MirrorPyPI, "/pypi", "ansible-core 2.19.14", false},
		{config.MirrorPyPI, "/empty", "doesn't list ansible-core", true},
	} {
		got, err := ProbeMirror(context.Background(), tc.kind, srv.URL+tc.path)
		if tc.err {
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("%s %s: got (%q, %v), want error containing %q", tc.kind, tc.path, got, err, tc.want)
			}
		} else if err != nil || !strings.Contains(got, tc.want) {
			t.Errorf("%s %s: got (%q, %v), want %q", tc.kind, tc.path, got, err, tc.want)
		}
	}
}
