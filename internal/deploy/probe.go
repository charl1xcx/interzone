package deploy

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptrace"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"interzone/internal/geo"
)

// The Test screen's checks beyond what federation and apps need: the
// network on the way to a server (its names, Iran's filter, where it is,
// IPv4 and IPv6, how fast it answers), its TLS, and the server itself
// (sign-ups, public rooms, Matrix features, out-of-date software).

// moreProbes are the network and TLS checks, then the server's own.
func moreProbes(ctx context.Context, domain string, fr fedResult, base string) (network, server []Finding) {
	// The names and ports apps and servers connect to, each once.
	var ends []string
	addEnd := func(hostport string) {
		if hostport != "" && !slices.Contains(ends, hostport) {
			ends = append(ends, hostport)
		}
	}
	if fr.dial != "" {
		addEnd(fr.dial)
	} else {
		addEnd(fr.at)
	}
	if u, err := url.Parse(base); err == nil && u.Scheme == "https" {
		port := u.Port()
		if port == "" {
			port = "443"
		}
		addEnd(net.JoinHostPort(u.Hostname(), port))
	}
	var hosts []string
	for _, e := range append([]string{domain}, ends...) {
		h := e
		if hh, _, err := net.SplitHostPort(e); err == nil {
			h = hh
		}
		if h = strings.Trim(h, "[]"); !slices.Contains(hosts, h) {
			hosts = append(hosts, h)
		}
	}

	var mu sync.Mutex
	var wg sync.WaitGroup
	addrs := map[string][]netip.Addr{}
	dns := make([]Finding, len(hosts))
	for i, h := range hosts {
		wg.Add(1)
		go func() {
			defer wg.Done()
			f, a := lookupHost(ctx, h)
			mu.Lock()
			dns[i], addrs[h] = f, a
			mu.Unlock()
		}()
	}
	wg.Wait()

	reach, certs := make([]Finding, len(ends)), make([]Finding, len(ends))
	var speed []Finding
	var hsts *Finding
	var vers struct {
		Versions []string        `json:"versions"`
		Unstable map[string]bool `json:"unstable_features"`
	}
	for i, e := range ends {
		h, port, _ := net.SplitHostPort(e)
		wg.Add(2)
		go func() { defer wg.Done(); reach[i] = reachBoth(ctx, e, addrs[strings.Trim(h, "[]")]) }()
		go func() { defer wg.Done(); certs[i] = checkTLS(ctx, strings.Trim(h, "[]"), port) }()
	}
	var signups, rooms Finding
	if base != "" {
		wg.Add(3)
		go func() {
			defer wg.Done()
			var h http.Header
			speed, h = timeIt(ctx, base+"/_matrix/client/versions", &vers)
			if strings.HasPrefix(base, "https://") {
				hsts = hstsFinding(h)
			}
		}()
		go func() { defer wg.Done(); signups = signUps(ctx, base) }()
		go func() { defer wg.Done(); rooms = publicRooms(ctx, base) }()
	}
	wg.Wait()

	network = slices.Concat(dns, reach, speed, certs)
	if hsts != nil {
		network = append(network, *hsts)
	}
	if base != "" {
		server = []Finding{signups, rooms}
		if vers.Versions != nil {
			server = append(server, features(vers.Versions, vers.Unstable))
		}
	}
	return network, server
}

// filterNet is where Iran's filter points the names it blocks
// (10.10.34.34, .35 and .36).
var filterNet = netip.MustParsePrefix("10.10.34.0/24")

// lookupHost resolves a name and says where each address is.
func lookupHost(ctx context.Context, h string) (Finding, []netip.Addr) {
	f := Finding{Group: "Network", Name: "DNS"}
	var addrs []netip.Addr
	if a, err := netip.ParseAddr(h); err == nil {
		addrs = []netip.Addr{a}
	} else {
		ips, err := net.DefaultResolver.LookupNetIP(ctx, "ip", h)
		if err != nil {
			f.Level, f.Detail = Fail, h+": "+netReason(err)
			return f, nil
		}
		for _, a := range ips {
			addrs = append(addrs, a.Unmap())
		}
	}
	for _, a := range addrs {
		if filterNet.Contains(a) {
			f.Level, f.Detail = Fail, fmt.Sprintf("%s → %s, the address Iran's filter gives the names it blocks", h, a)
			return f, nil
		}
	}
	var parts []string
	for _, v6 := range []bool{false, true} { // the first IPv4 and IPv6 address, where they are
		for _, a := range addrs {
			if a.Is6() == v6 {
				if w := where(a); w != "" {
					parts = append(parts, a.String()+" ("+w+")")
				} else {
					parts = append(parts, a.String())
				}
				if !a.IsGlobalUnicast() || a.IsPrivate() {
					f.Level = Warn
				}
				break
			}
		}
	}
	f.Detail = h + " → " + strings.Join(parts, ", ")
	if f.Level == Warn {
		f.Detail += "; a private address can't be reached from outside its network"
	}
	return f, addrs
}

// where is a place for an address: a town and network in Iran, else a
// country; "" if unknown (the data has only Iran's IPv6 addresses).
func where(a netip.Addr) string {
	p, ok := geo.Locate(a)
	switch {
	case a.IsLoopback():
		return "this computer"
	case !ok && a.IsPrivate():
		return "private"
	case !ok:
		return ""
	case p.Country == "IR" && p.ISP != "":
		return p.Town + ", " + p.ISP
	case p.Country == "IR":
		return p.Town
	}
	return p.Country + ", abroad"
}

// reachBoth connects to hostport over IPv4 and IPv6, where the name has
// such an address. It connects directly, not through a proxy, as other
// servers would.
func reachBoth(ctx context.Context, hostport string, addrs []netip.Addr) Finding {
	f := Finding{Group: "Network", Name: "IPv4 and IPv6"}
	if addrs == nil {
		f.Level, f.Detail = Fail, hostport+": no address to connect to"
		return f
	}
	_, port, _ := net.SplitHostPort(hostport)
	var parts []string
	ok := 0
	for _, v6 := range []bool{false, true} {
		name := map[bool]string{false: "IPv4", true: "IPv6"}[v6]
		i := slices.IndexFunc(addrs, func(a netip.Addr) bool { return a.Is6() == v6 })
		if i < 0 {
			parts = append(parts, "no "+name+" address")
			continue
		}
		d := net.Dialer{Timeout: 5 * time.Second}
		c, err := d.DialContext(ctx, "tcp", net.JoinHostPort(addrs[i].String(), port))
		if v6 && errors.Is(err, syscall.ENETUNREACH) {
			parts = append(parts, "IPv6 untested: this computer has none")
			continue
		}
		if err != nil {
			parts = append(parts, name+" "+netReason(err))
			if v6 && f.Level == Pass {
				f.Level = Warn
			}
			continue
		}
		c.Close()
		ok++
		parts = append(parts, name+" connects")
	}
	if ok == 0 {
		f.Level = Fail
	}
	f.Detail = hostport + ": " + strings.Join(parts, ", ")
	return f
}

// timeIt GETs url, decoding its JSON into v, and says how long each step
// took from here.
func timeIt(ctx context.Context, u string, v any) ([]Finding, http.Header) {
	f := Finding{Group: "Network", Name: "Speed"}
	var start, dnsAt, connAt, tlsAt, firstAt time.Time
	trace := &httptrace.ClientTrace{
		DNSDone:              func(httptrace.DNSDoneInfo) { dnsAt = time.Now() },
		ConnectDone:          func(_, _ string, err error) { connAt = time.Now() },
		TLSHandshakeDone:     func(tls.ConnectionState, error) { tlsAt = time.Now() },
		GotFirstResponseByte: func() { firstAt = time.Now() },
	}
	req, err := http.NewRequestWithContext(httptrace.WithClientTrace(ctx, trace), http.MethodGet, u, nil)
	if err != nil {
		return nil, nil
	}
	start = time.Now()
	resp, err := httpClient(false, "").Do(req)
	if err != nil {
		return nil, nil // the Client API row says why
	}
	defer resp.Body.Close()
	_ = json.NewDecoder(resp.Body).Decode(v)
	ms := func(t time.Time) string { return fmt.Sprintf("%d ms", t.Sub(start).Milliseconds()) }
	var parts []string
	for _, s := range []struct {
		name string
		at   time.Time
	}{{"name", dnsAt}, {"connected", connAt}, {"TLS", tlsAt}, {"answer", firstAt}} {
		if !s.at.IsZero() {
			parts = append(parts, s.name+" "+ms(s.at))
		}
	}
	f.Detail = strings.Join(parts, " · ")
	if p, _ := http.ProxyFromEnvironment(req); p != nil {
		f.Detail += " (through the proxy)"
	}
	if firstAt.Sub(start) > 3*time.Second {
		f.Level = Warn
		f.Detail += ": slow"
	}
	return []Finding{f}, resp.Header
}

// checkTLS looks at a server's certificate, and whether it still accepts
// TLS 1.0 or 1.1, which browsers and apps dropped.
func checkTLS(ctx context.Context, host, port string) Finding {
	f := Finding{Group: "TLS", Name: "Certificate"}
	at := net.JoinHostPort(host, port)
	d := tls.Dialer{NetDialer: &net.Dialer{Timeout: 8 * time.Second}, Config: &tls.Config{ServerName: host, InsecureSkipVerify: true}}
	c, err := d.DialContext(ctx, "tcp", at)
	if err != nil {
		f.Level, f.Detail = Fail, at+": "+netReason(err)
		return f
	}
	st := c.(*tls.Conn).ConnectionState()
	c.Close()
	leaf := st.PeerCertificates[0]
	inter := x509.NewCertPool()
	for _, ic := range st.PeerCertificates[1:] {
		inter.AddCert(ic)
	}
	issuer := strings.Join(leaf.Issuer.Organization, " ")
	if leaf.Issuer.CommonName != "" {
		issuer = strings.TrimSpace(issuer + " " + leaf.Issuer.CommonName)
	}
	left := time.Until(leaf.NotAfter)
	f.Detail = fmt.Sprintf("%s: %s, %s, %d days left", at, tls.VersionName(st.Version), issuer, int(left.Hours()/24))
	if _, err := leaf.Verify(x509.VerifyOptions{DNSName: host, Roots: rootCAs, Intermediates: inter}); err != nil {
		f.Level, f.Detail = Fail, fmt.Sprintf("%s: the certificate isn't trusted: %s", at, err)
		return f
	}
	if left < 14*24*time.Hour {
		f.Level = Warn
		f.Detail += "; renew it soon"
	}
	old := tls.Dialer{NetDialer: &net.Dialer{Timeout: 8 * time.Second},
		Config: &tls.Config{ServerName: host, InsecureSkipVerify: true, MinVersion: tls.VersionTLS10, MaxVersion: tls.VersionTLS11}}
	if c, err := old.DialContext(ctx, "tcp", at); err == nil {
		c.Close()
		f.Level = Warn
		f.Detail += "; still accepts TLS 1.0/1.1, which should be off"
	}
	return f
}

// hstsFinding says whether the client API tells browsers to use HTTPS only.
func hstsFinding(h http.Header) *Finding {
	f := &Finding{Group: "TLS", Name: "HSTS"}
	v := h.Get("Strict-Transport-Security")
	m := regexp.MustCompile(`max-age=(\d+)`).FindStringSubmatch(v)
	if m == nil {
		f.Detail = "not sent (optional: it has browsers refuse plain HTTP)"
		return f
	}
	n, _ := strconv.Atoi(m[1])
	f.Detail = fmt.Sprintf("browsers keep to HTTPS for %d days", n/86400)
	return f
}

// signUps asks to register with no details, which never makes an account:
// the answer says whether sign-up is open and what it asks for.
func signUps(ctx context.Context, base string) Finding {
	f := Finding{Group: "Server", Name: "Sign-ups"}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/_matrix/client/v3/register", bytes.NewReader([]byte("{}")))
	if err != nil {
		return f
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := httpClient(false, "").Do(req)
	if err != nil {
		f.Level, f.Detail = Warn, "can't tell: "+netReason(err)
		return f
	}
	defer resp.Body.Close()
	var r struct {
		Errcode string `json:"errcode"`
		Flows   []struct {
			Stages []string `json:"stages"`
		} `json:"flows"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&r)
	switch {
	case resp.StatusCode == http.StatusUnauthorized && len(r.Flows) > 0:
	case r.Errcode == "M_FORBIDDEN":
		f.Detail = "closed"
		var meta map[string]any
		if _, err := getJSON(ctx, base+"/_matrix/client/v1/auth_metadata", false, &meta); err == nil {
			f.Detail = "not through the client API; through its OAuth 2.0 login page, if at all"
		}
		return f
	case resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusMethodNotAllowed || r.Errcode == "M_UNRECOGNIZED":
		f.Detail = "not through the client API; through its OAuth 2.0 login page, if at all"
		return f
	default:
		f.Detail = "can't tell: answered " + resp.Status
		return f
	}
	// What the easiest way in asks for; a token is the hardest to get.
	names := map[string]string{"m.login.registration_token": "a sign-up token", "m.login.email.identity": "an email address",
		"m.login.msisdn": "a phone number", "m.login.recaptcha": "a captcha", "m.login.terms": "agreeing to terms"}
	var easiest []string
	hard := func(asks []string) int {
		if slices.Contains(asks, names["m.login.registration_token"]) {
			return 100
		}
		return len(asks)
	}
	for i, fl := range r.Flows {
		var asks []string
		for _, st := range fl.Stages {
			if n, ok := names[st]; ok && !slices.Contains(asks, n) {
				asks = append(asks, n)
			}
		}
		if i == 0 || hard(asks) < hard(easiest) {
			easiest = asks
		}
	}
	gated := slices.ContainsFunc(easiest, func(n string) bool { return n != "agreeing to terms" })
	switch {
	case !gated:
		f.Level, f.Detail = Warn, "open to anyone, with no token, email or captcha: spam bots can sign up"
	case slices.Contains(easiest, "a sign-up token"):
		f.Detail = "with " + strings.Join(easiest, ", ") + " only"
	default:
		f.Detail = "open, with " + strings.Join(easiest, ", ")
	}
	return f
}

// publicRooms counts the rooms in the server's public directory.
func publicRooms(ctx context.Context, base string) Finding {
	f := Finding{Group: "Server", Name: "Public rooms"}
	var r struct {
		Chunk []json.RawMessage `json:"chunk"`
		Total *int              `json:"total_room_count_estimate"`
	}
	_, err := getJSON(ctx, base+"/_matrix/client/v3/publicRooms?limit=1", false, &r)
	switch {
	case err != nil && strings.Contains(err.Error(), "401") || err != nil && strings.Contains(err.Error(), "403"):
		f.Detail = "only signed-in users can see its room directory"
	case err != nil:
		f.Detail = "its room directory " + err.Error()
	case r.Total != nil:
		f.Detail = plural(*r.Total, "room") + " in its public directory"
	case len(r.Chunk) == 0:
		f.Detail = "none in its public directory"
	default:
		f.Detail = "its public directory has some"
	}
	return f
}

// features names what the server's Matrix versions bring that people notice.
func features(versions []string, unstable map[string]bool) Finding {
	f := Finding{Group: "Server", Name: "Features"}
	var parts []string
	for _, ft := range []struct{ name, since, flag string }{
		{"spaces", "v1.2", ""}, {"threads", "v1.4", ""}, {"authenticated media", "v1.11", "org.matrix.msc3916.stable"},
	} {
		mark := "✗ "
		if slices.Contains(versions, ft.since) || unstable[ft.flag] {
			mark = "✓ "
		}
		parts = append(parts, mark+ft.name)
	}
	f.Detail = strings.Join(parts, "  ")
	return f
}

// outdated marks the Software row when the server runs an older version
// than the playbook in playbookDir would install.
func outdated(fed []Finding, fr fedResult, playbookDir string) {
	if playbookDir == "" || fr.software == "" {
		return
	}
	n := strings.ToLower(strings.ReplaceAll(fr.software, " ", ""))
	b, err := os.ReadFile(filepath.Join(playbookDir, "roles", "custom", "matrix-"+n, "defaults", "main.yml"))
	if err != nil {
		return
	}
	m := regexp.MustCompile(`(?m)^matrix_` + regexp.QuoteMeta(n) + `_version:\s*"?v?([0-9][^"\s]*)`).FindSubmatch(b)
	if m == nil {
		return
	}
	pinned := string(m[1])
	have := strings.TrimPrefix(strings.Fields(fr.version + " ")[0], "v")
	for i := range fed {
		if fed[i].Name != "Software" {
			continue
		}
		switch c := cmpVersions(have, pinned); {
		case c < 0:
			fed[i].Level = Warn
			fed[i].Detail += fmt.Sprintf(": older than %s, which the playbook installs now", pinned)
		case c == 0:
			fed[i].Detail += ", the newest the playbook installs"
		}
	}
}

// cmpVersions compares dotted version numbers, 1.9 before 1.10.
func cmpVersions(a, b string) int {
	num := regexp.MustCompile(`\d+`)
	x, y := num.FindAllString(a, 4), num.FindAllString(b, 4)
	for i := range min(len(x), len(y)) {
		p, _ := strconv.Atoi(x[i])
		q, _ := strconv.Atoi(y[i])
		if p != q {
			return p - q
		}
	}
	return len(x) - len(y)
}

func plural(n int, what string) string {
	if n == 1 {
		return "1 " + what
	}
	return fmt.Sprintf("%d %ss", n, what)
}
