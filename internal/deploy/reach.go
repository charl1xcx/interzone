package deploy

import (
	"context"
	"crypto/tls"
	"crypto/x509"
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

// federationChecks does what a federation tester does for this server.
func federationChecks(ctx context.Context, c *config.Config) []Finding {
	if !c.Federation || !c.Installed() {
		return nil
	}
	out, _ := federationProbe(ctx, c.Domain)
	return out
}

// federationProbe finds where other servers would reach domain, as the
// spec has them do (a port in the name, else /.well-known/matrix/server,
// else an SRV record, else port 8448), and asks it for its signing keys
// and software.
func federationProbe(ctx context.Context, domain string) ([]Finding, fedResult) {
	var out []Finding
	add := func(name string, l Level, format string, a ...any) {
		out = append(out, Finding{Group: "Federation", Name: name, Level: l, Detail: fmt.Sprintf(format, a...)})
	}
	at, dial := "", ""
	if _, _, err := net.SplitHostPort(domain); err == nil {
		at = domain
		add("Delegation", Pass, "the name has a port, so servers go straight to %s", at)
	} else if net.ParseIP(strings.Trim(domain, "[]")) != nil {
		at = domain + ":8448"
		add("Delegation", Pass, "an IP address: servers go straight to %s", at)
	}
	if at == "" {
		var wk struct {
			Server string `json:"m.server"`
		}
		_, err := getJSON(ctx, "https://"+domain+"/.well-known/matrix/server", false, &wk)
		if err == nil && wk.Server == "" {
			err = errors.New("has no m.server")
		}
		if err == nil {
			// shortcut: a delegated name without a port isn't looked up in SRV again; few servers rely on that.
			at = wk.Server
			if _, _, e := net.SplitHostPort(at); e != nil {
				at += ":8448"
			}
			add("Delegation", Pass, "/.well-known/matrix/server sends servers to %s", at)
		} else if target, ok := lookupFedSRV(ctx, domain); ok {
			at, dial = domain, target
			add("Delegation", Pass, "no well-known (%s), but an SRV record sends servers to %s", err, target)
		} else {
			at = domain + ":8448"
			add("Delegation", Warn, "https://%s/.well-known/matrix/server %s and there's no SRV record; servers will try %s", domain, err, at)
		}
	}

	var keys struct {
		Name       string                     `json:"server_name"`
		ValidUntil int64                      `json:"valid_until_ts"`
		Verify     map[string]json.RawMessage `json:"verify_keys"`
	}
	switch _, err := fetchJSON(ctx, "https://"+at+"/_matrix/key/v2/server", false, dial, &keys); {
	case err != nil:
		add("Federation API", Fail, "https://%s %s; other servers can't reach this one", at, err)
		return out, fedResult{at: at, dial: dial}
	case keys.Name != domain:
		add("Federation API", Fail, "%s answers as %q, not %s", at, keys.Name, domain)
	case len(keys.Verify) == 0:
		add("Federation API", Fail, "%s answers, but publishes no signing key", at)
	case keys.ValidUntil < time.Now().UnixMilli():
		add("Federation API", Warn, "%s answers, but its signing keys expired %s", at, time.UnixMilli(keys.ValidUntil).Format("2006-01-02 15:04"))
	default:
		add("Federation API", Pass, "%s answers as %s, with a trusted certificate", at, keys.Name)
	}
	var v struct {
		Server struct{ Name, Version string } `json:"server"`
	}
	if _, err := fetchJSON(ctx, "https://"+at+"/_matrix/federation/v1/version", false, dial, &v); err != nil || v.Server.Name == "" {
		add("Software", Warn, "/_matrix/federation/v1/version doesn't say")
	} else {
		add("Software", Pass, "%s %s", v.Server.Name, v.Server.Version)
	}
	return out, fedResult{at, dial, v.Server.Name, v.Server.Version}
}

// fedResult is where federation reaches a server, and what answers there.
type fedResult struct{ at, dial, software, version string }

// lookupFedSRV is the SRV record federation uses, or the older one.
func lookupFedSRV(ctx context.Context, domain string) (string, bool) {
	for _, svc := range []string{"matrix-fed", "matrix"} {
		if _, srvs, err := net.DefaultResolver.LookupSRV(ctx, svc, "tcp", domain); err == nil && len(srvs) > 0 {
			return net.JoinHostPort(strings.TrimSuffix(srvs[0].Target, "."), fmt.Sprint(srvs[0].Port)), true
		}
	}
	return "", false
}

// rootCAs are the certificates trusted; nil is the system's. Tests set it.
var rootCAs *x509.CertPool

// getJSON GETs url and decodes its JSON answer into v.
func getJSON(ctx context.Context, url string, insecure bool, v any) (http.Header, error) {
	return fetchJSON(ctx, url, insecure, "", v)
}

// fetchJSON is getJSON connecting to dial (host:port) instead of the URL's
// host, as SRV delegation does: the certificate is still checked for it.
func fetchJSON(ctx context.Context, url string, insecure bool, dial string, v any) (http.Header, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := httpClient(insecure, dial).Do(req)
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

// httpClient is how the checks ask servers, through the proxy if one is
// set, unless dial (host:port) says where to connect.
func httpClient(insecure bool, dial string) *http.Client {
	tr := &http.Transport{Proxy: http.ProxyFromEnvironment, TLSClientConfig: &tls.Config{InsecureSkipVerify: insecure, RootCAs: rootCAs}}
	if dial != "" {
		tr.Proxy = nil
		tr.DialContext = func(ctx context.Context, network, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, network, dial)
		}
	}
	return &http.Client{Timeout: 10 * time.Second, Transport: tr}
}

// clientChecks looks at this server as apps do.
func clientChecks(ctx context.Context, c *config.Config) []Finding {
	if !c.Installed() {
		return nil
	}
	out, _ := clientProbe(ctx, c, true)
	return out
}

// clientProbe looks at a server as apps do: /.well-known/matrix/client
// sends them to the homeserver, which says what it speaks and how to log
// in; Element X needs more than older apps: sliding sync, OAuth 2.0 logins
// (MAS), and for calls Matrix RTC. own says it's the server interzone set
// up, whose settings explain what's missing.
func clientProbe(ctx context.Context, c *config.Config, own bool) ([]Finding, string) {
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
	base := scheme + c.Domain // where apps look without a well-known
	if own {
		base = scheme + c.HostName()
	}
	switch h, err := getJSON(ctx, where, insecure, &wk); {
	case err != nil && own:
		add("Well-known", Warn, "%s %s; apps then need %s typed in instead of %s", where, err, c.HostName(), c.Domain)
	case err != nil:
		add("Well-known", Warn, "%s %s; apps then try %s itself", where, err, base)
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
		Versions []string        `json:"versions"`
		Unstable map[string]bool `json:"unstable_features"`
	}
	if _, err := getJSON(ctx, base+"/_matrix/client/versions", insecure, &versions); err != nil {
		add("Client API", Fail, "%s/_matrix/client/versions %s", base, err)
		return out, base
	}
	newest := "no spec versions"
	if n := len(versions.Versions); n > 0 {
		newest = "spec up to " + versions.Versions[n-1]
	}
	add("Client API", Pass, "%s answers, %s", base, newest)

	var login struct {
		Flows []struct {
			Type string `json:"type"`
		} `json:"flows"`
	}
	if _, err := getJSON(ctx, base+"/_matrix/client/v3/login", insecure, &login); err != nil || len(login.Flows) == 0 {
		add("Logins", Warn, "%s/_matrix/client/v3/login lists no way to log in", base)
	} else {
		var types []string
		for _, f := range login.Flows {
			types = append(types, strings.TrimPrefix(f.Type, "m.login."))
		}
		add("Logins", Pass, "%s", strings.Join(types, ", "))
	}

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
	} else if own {
		add("Element X login", Warn, "no OAuth 2.0 logins: Element X needs the Matrix Authentication Service (MAS) on Components")
	} else {
		add("Element X login", Warn, "no OAuth 2.0 logins, which Element X needs")
	}
	rtc := c.Components["matrix-rtc"] || c.Components["element-call"]
	switch {
	case len(wk.Foci) > 0:
		add("Element X calls", Pass, "the well-known names a call server")
	case own && rtc:
		add("Element X calls", Fail, "Matrix RTC is on, but the well-known names no call server; deploy again")
	case own:
		add("Element X calls", Warn, "calls need the Matrix RTC stack or Element Call on Components")
	default:
		add("Element X calls", Warn, "the well-known names no call server, so Element X can't call")
	}

	var support struct {
		Contacts []json.RawMessage `json:"contacts"`
		Page     string            `json:"support_page"`
	}
	if _, err := getJSON(ctx, scheme+c.Domain+"/.well-known/matrix/support", insecure, &support); err == nil && (len(support.Contacts) > 0 || support.Page != "") {
		add("Support", Pass, "/.well-known/matrix/support lists %d contact(s) %s", len(support.Contacts), support.Page)
	} else if !own {
		add("Support", Pass, "no /.well-known/matrix/support (optional)")
	}
	return out, base
}

// ProbeServer looks at any Matrix server from here, as other servers and
// apps do, then at the network and TLS on the way to it and at the server
// itself (probe.go). playbookDir, if set, is the playbook whose pinned
// versions say whether its software is out of date.
func ProbeServer(ctx context.Context, domain, playbookDir string) []Finding {
	c := &config.Config{Domain: domain, SSL: config.SSL{Mode: "letsencrypt"}}
	var fed, cl []Finding
	var fr fedResult
	var base string
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); fed, fr = federationProbe(ctx, domain) }()
	go func() { defer wg.Done(); cl, base = clientProbe(ctx, c, false) }()
	wg.Wait()
	outdated(fed, fr, playbookDir)
	network, server := moreProbes(ctx, domain, fr, base)
	return append(append(append(network, fed...), cl...), server...)
}

// ServerName is the Matrix server a user ID, URL or name points at:
// @alice:example.org, https://example.org/x and example.org are all
// example.org.
func ServerName(s string) string {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "@") || strings.HasPrefix(s, "#") || strings.HasPrefix(s, "!") {
		_, s, _ = strings.Cut(s, ":")
	}
	if _, rest, ok := strings.Cut(s, "://"); ok {
		s = rest
	}
	s, _, _ = strings.Cut(s, "/")
	return strings.ToLower(strings.TrimSuffix(s, "."))
}
