package deploy

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"syscall"
	"time"

	"interzone/internal/config"
)

// reachChecks looks at the server from here, as the internet would: whether
// the TCP ports the components need get through (UDP can't be told from
// silence), and whether other servers can federate with it.
func reachChecks(ctx context.Context, c *config.Config) []Finding {
	if c.Domain == "" {
		return nil
	}
	out := append(portChecks(ctx, c), federationChecks(ctx, c)...)
	return append(out, clientChecks(ctx, c)...)
}

// portChecks connects to every TCP port Ports lists, at the homeserver's
// hostname. Refused means the port got through to a server with nothing
// listening on it; no answer at all usually means a firewall drops it.
func portChecks(ctx context.Context, c *config.Config) []Finding {
	host := c.HostName()
	if _, err := net.DefaultResolver.LookupHost(ctx, host); err != nil {
		return nil // the DNS checks already say so
	}
	installed := c.Installed()
	var out []Finding
	for _, p := range c.Ports() {
		for _, port := range p.TCP {
			out = append(out, Finding{Group: "Ports", Name: port + "/tcp", Service: p.For})
		}
	}
	var wg sync.WaitGroup
	for i := range out {
		wg.Add(1)
		go func(f *Finding) {
			defer wg.Done()
			f.Level, f.Detail = dialPort(ctx, host, strings.TrimSuffix(f.Name, "/tcp"), installed)
		}(&out[i])
	}
	wg.Wait()
	return out
}

// dialPort says whether a TCP connection to host:port gets through.
func dialPort(ctx context.Context, host, port string, installed bool) (Level, string) {
	d := net.Dialer{Timeout: 6 * time.Second}
	conn, err := d.DialContext(ctx, "tcp", net.JoinHostPort(host, port))
	switch {
	case err == nil:
		conn.Close()
		return Pass, "open"
	case errors.Is(err, syscall.ECONNREFUSED) && installed:
		return Warn, "gets through, but nothing answers: is the service running?"
	case errors.Is(err, syscall.ECONNREFUSED):
		return Pass, "gets through; nothing listens until you deploy"
	case os.IsTimeout(err) || errors.Is(err, context.DeadlineExceeded):
		return Warn, "no answer: a firewall in front of the server probably drops it"
	}
	return Warn, netReason(err)
}

// federationChecks does what a federation tester does: finds where other
// servers would reach this one (/.well-known/matrix/server, else port
// 8448 of the domain) and asks it for the server's signing keys.
func federationChecks(ctx context.Context, c *config.Config) []Finding {
	if !c.Federation || !c.Installed() {
		return nil
	}
	var out []Finding
	add := func(name string, l Level, format string, a ...any) {
		out = append(out, Finding{Group: "Federation", Name: name, Level: l, Detail: fmt.Sprintf(format, a...)})
	}
	get := func(url string, v any) error {
		_, err := getJSON(ctx, url, false, v)
		return err
	}

	// shortcut: SRV records aren't looked up; the playbook delegates with .well-known.
	at := c.Domain + ":8448"
	var wk struct {
		Server string `json:"m.server"`
	}
	if err := get("https://"+c.Domain+"/.well-known/matrix/server", &wk); err != nil || wk.Server == "" {
		if err == nil {
			err = errors.New("has no m.server")
		}
		add("Delegation", Warn, "https://%s/.well-known/matrix/server %s; other servers will try %s", c.Domain, err, at)
	} else {
		at = wk.Server
		if _, _, err := net.SplitHostPort(at); err != nil {
			at += ":8448"
		}
		add("Delegation", Pass, "other servers are sent to %s", at)
	}

	var keys struct {
		Name string `json:"server_name"`
	}
	switch err := get("https://"+at+"/_matrix/key/v2/server", &keys); {
	case err != nil:
		add("Federation API", Fail, "https://%s %s; other servers can't reach this one", at, err)
	case keys.Name != c.Domain:
		add("Federation API", Fail, "%s answers as %q, not %s", at, keys.Name, c.Domain)
	default:
		add("Federation API", Pass, "%s answers as %s, with a trusted certificate", at, c.Domain)
	}
	return out
}

// getJSON GETs url and decodes its JSON answer into v.
func getJSON(ctx context.Context, url string, insecure bool, v any) (http.Header, error) {
	client := &http.Client{Timeout: 10 * time.Second, Transport: &http.Transport{Proxy: http.ProxyFromEnvironment,
		TLSClientConfig: &tls.Config{InsecureSkipVerify: insecure}}}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, errors.New(netReason(err))
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return resp.Header, errors.New("answered " + resp.Status)
	}
	if json.NewDecoder(resp.Body).Decode(v) != nil {
		return resp.Header, errors.New("didn't answer JSON")
	}
	return resp.Header, nil
}

// clientChecks looks at the server as apps do: /.well-known/matrix/client
// sends them to the homeserver, and Element X needs more than older apps:
// sliding sync, OAuth 2.0 logins (MAS), and for calls Matrix RTC.
func clientChecks(ctx context.Context, c *config.Config) []Finding {
	if !c.Installed() {
		return nil
	}
	var out []Finding
	add := func(name string, l Level, format string, a ...any) {
		out = append(out, Finding{Group: "Clients", Name: name, Level: l, Detail: fmt.Sprintf(format, a...)})
	}
	scheme, insecure := "https://", c.SSL.Mode == "selfsigned"
	if c.SSL.Mode == "none" {
		scheme = "http://"
	}
	var wk struct {
		Homeserver struct {
			BaseURL string `json:"base_url"`
		} `json:"m.homeserver"`
		Auth *struct {
			Issuer string `json:"issuer"`
		} `json:"org.matrix.msc2965.authentication"`
		Foci []json.RawMessage `json:"org.matrix.msc4143.rtc_foci"`
	}
	where := scheme + c.Domain + "/.well-known/matrix/client"
	base := scheme + c.HostName()
	switch h, err := getJSON(ctx, where, insecure, &wk); {
	case err != nil:
		add("Well-known", Warn, "%s %s; apps then need %s typed in instead of %s", where, err, c.HostName(), c.Domain)
	case wk.Homeserver.BaseURL == "":
		add("Well-known", Fail, "%s has no m.homeserver.base_url", where)
	default:
		base = strings.TrimRight(wk.Homeserver.BaseURL, "/")
		if h.Get("Access-Control-Allow-Origin") == "" {
			add("Well-known", Warn, "%s sends apps to %s, but without the CORS header web apps need", where, base)
		} else {
			add("Well-known", Pass, "sends apps to %s", base)
		}
	}

	var versions struct {
		Unstable map[string]bool `json:"unstable_features"`
	}
	if _, err := getJSON(ctx, base+"/_matrix/client/versions", insecure, &versions); err != nil {
		add("Client API", Fail, "%s/_matrix/client/versions %s", base, err)
		return out
	}
	add("Client API", Pass, "%s answers", base)

	if versions.Unstable["org.matrix.simplified_msc3575"] {
		add("Element X sync", Pass, "sliding sync is on")
	} else {
		add("Element X sync", Fail, "the homeserver doesn't offer sliding sync, which Element X needs")
	}
	var meta map[string]any
	if wk.Auth != nil && wk.Auth.Issuer != "" {
		add("Element X login", Pass, "OAuth 2.0 logins through %s", wk.Auth.Issuer)
	} else if _, err := getJSON(ctx, base+"/_matrix/client/v1/auth_metadata", insecure, &meta); err == nil {
		add("Element X login", Pass, "OAuth 2.0 logins")
	} else {
		add("Element X login", Warn, "no OAuth 2.0 logins: Element X needs the Matrix Authentication Service (MAS) on Components")
	}
	rtc := c.Components["matrix-rtc"] || c.Components["element-call"]
	switch {
	case len(wk.Foci) > 0:
		add("Element X calls", Pass, "the well-known names a call server")
	case rtc:
		add("Element X calls", Fail, "Matrix RTC is on, but the well-known names no call server; deploy again")
	default:
		add("Element X calls", Warn, "calls need the Matrix RTC stack or Element Call on Components")
	}
	return out
}
