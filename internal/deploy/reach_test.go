package deploy

import (
	"context"
	"fmt"
	"maps"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"interzone/internal/config"
)

func TestDialPort(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	_, open, _ := net.SplitHostPort(l.Addr().String())
	l.Close() // now refused
	if lv, d := dialPort(context.Background(), "127.0.0.1", open, true); lv != Warn {
		t.Errorf("closed port, installed: %v %s", lv, d)
	}
	if lv, d := dialPort(context.Background(), "127.0.0.1", open, false); lv != Pass {
		t.Errorf("closed port, not installed: %v %s", lv, d)
	}
	l, _ = net.Listen("tcp", "127.0.0.1:"+open)
	defer l.Close()
	if lv, d := dialPort(context.Background(), "127.0.0.1", open, true); lv != Pass || d != "open" {
		t.Errorf("open port: %v %s", lv, d)
	}
}

func TestClientChecks(t *testing.T) {
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/.well-known/matrix/client": // no CORS header, no auth, no call server
			fmt.Fprintf(w, `{"m.homeserver": {"base_url": %q}}`, srv.URL)
		case "/_matrix/client/versions":
			fmt.Fprint(w, `{"versions": ["v1.11"], "unstable_features": {}}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	c := config.Default()
	c.Domain, c.SSL.Mode = strings.TrimPrefix(srv.URL, "http://"), "none"
	c.Components = map[string]bool{"matrix-rtc": true}
	c.PlaybookDir = t.TempDir()
	vy := filepath.Join(c.PlaybookDir, "inventory", "host_vars", c.HostName(), "vars.yml")
	_ = os.MkdirAll(filepath.Dir(vy), 0o755)
	_ = os.WriteFile(vy, nil, 0o644)
	got := map[string]Level{}
	for _, f := range clientChecks(context.Background(), c) {
		got[f.Name] = f.Level
	}
	want := map[string]Level{"Well-known": Warn, "Client API": Pass, "Element X sync": Fail, "Element X login": Warn, "Element X calls": Fail}
	if !maps.Equal(got, want) {
		t.Errorf("got %v", got)
	}
}
