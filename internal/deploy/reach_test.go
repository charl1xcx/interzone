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
	want := map[string]Level{"Well-known": Warn, "Client API": Pass, "Logins": Warn, "Element X sync": Fail, "Element X login": Warn, "Element X calls": Fail}
	if !maps.Equal(got, want) {
		t.Errorf("got %v", got)
	}
}

func TestZoneRecords(t *testing.T) {
	c := config.Default()
	c.Domain, c.Homeserver = "example.org", "synapse"
	c.Components = map[string]bool{}
	c.Target = config.Target{Mode: "ssh", Host: "203.0.113.7", User: "root"}
	got := strings.Join(ZoneRecords(context.Background(), c), "\n")
	if !strings.Contains(got, "matrix.example.org. 3600 IN A    203.0.113.7") || !strings.Contains(got, "/.well-known/matrix/server") {
		t.Errorf("got\n%s", got)
	}
	c.Target.Mode = "local"
	if got := strings.Join(ZoneRecords(context.Background(), c), "\n"); !strings.Contains(got, "IN A    SERVER_IP") {
		t.Errorf("local:\n%s", got)
	}
}

// A server is probed as others see it: its keys and software through
// federation, its well-known and client API as apps do.
func TestProbeServer(t *testing.T) {
	var srv *httptest.Server
	srv = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		switch r.URL.Path {
		case "/_matrix/key/v2/server":
			fmt.Fprintf(w, `{"server_name": %q, "valid_until_ts": 99999999999999, "verify_keys": {"ed25519:a": {"key": "k"}}}`, r.Host)
		case "/_matrix/federation/v1/version":
			fmt.Fprint(w, `{"server": {"name": "Synapse", "version": "1.140.0"}}`)
		case "/.well-known/matrix/client":
			fmt.Fprintf(w, `{"m.homeserver": {"base_url": %q}, "org.matrix.msc4143.rtc_foci": [{}]}`, srv.URL)
		case "/_matrix/client/versions":
			fmt.Fprint(w, `{"versions": ["v1.11", "v1.12"], "unstable_features": {"org.matrix.simplified_msc3575": true}}`)
		case "/_matrix/client/v3/login":
			fmt.Fprint(w, `{"flows": [{"type": "m.login.password"}, {"type": "m.login.sso"}]}`)
		case "/_matrix/client/v1/auth_metadata":
			fmt.Fprint(w, `{}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	rootCAs = srv.Client().Transport.(*http.Transport).TLSClientConfig.RootCAs
	defer func() { rootCAs = nil }()

	name := ServerName("@alice:" + strings.TrimPrefix(srv.URL, "https://"))
	got := ProbeServer(context.Background(), name)
	var lines []string
	for _, f := range got {
		lines = append(lines, fmt.Sprintf("%d %s: %s", f.Level, f.Name, f.Detail))
	}
	all := strings.Join(lines, "\n")
	for _, want := range []string{"0 Delegation: the name has a port", "0 Federation API: " + name + " answers as " + name,
		"0 Software: Synapse 1.140.0", "0 Well-known: sends apps to " + srv.URL, "spec up to v1.12", "0 Logins: password, sso",
		"0 Element X sync", "0 Element X login", "0 Element X calls", "0 Support: no /.well-known/matrix/support"} {
		if !strings.Contains(all, want) {
			t.Errorf("no %q in\n%s", want, all)
		}
	}
}

func TestServerName(t *testing.T) {
	for in, want := range map[string]string{"@a:Example.org": "example.org", "https://example.org/x": "example.org",
		" example.org:8448 ": "example.org:8448", "#room:matrix.org": "matrix.org"} {
		if got := ServerName(in); got != want {
			t.Errorf("%q: %q", in, got)
		}
	}
}
