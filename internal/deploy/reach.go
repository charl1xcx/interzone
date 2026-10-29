package deploy

import (
	"context"
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
	return append(portChecks(ctx, c), federationChecks(ctx, c)...)
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
	client := &http.Client{Timeout: 10 * time.Second, Transport: &http.Transport{Proxy: http.ProxyFromEnvironment}}
	get := func(url string, v any) error {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return err
		}
		resp, err := client.Do(req)
		if err != nil {
			return errors.New(netReason(err))
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return errors.New("answered " + resp.Status)
		}
		if json.NewDecoder(resp.Body).Decode(v) != nil {
			return errors.New("didn't answer JSON")
		}
		return nil
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
