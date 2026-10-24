package deploy

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"interzone/internal/config"
)

var (
	releaseDate = regexp.MustCompile(`(?m)^Date:\s*(.+)$`)
	repomdRev   = regexp.MustCompile(`<revision>(\d+)</revision>`)
	ansibleFile = regexp.MustCompile(`ansible[_-]core-(\d+\.\d+\.\d+)`)
)

// RefVersions are the releases a mirror must carry to count as covering a
// distro, when no particular server is being checked.
var RefVersions = map[config.MirrorKind]map[string][]string{
	config.MirrorOS: {
		config.Ubuntu:       {"noble", "jammy"},
		config.UbuntuPorts:  {"noble"},
		config.Debian:       {"trixie", "bookworm"},
		config.Raspbian:     {"bookworm"},
		config.AlmaLinux:    {"9", "10"},
		config.Rocky:        {"9", "10"},
		config.CentOSStream: {"9", "10"},
		config.Fedora:       {"44"},
		config.EPEL:         {"9", "10"},
		config.Arch:         {""},
	},
	config.MirrorDocker: {
		config.Debian:   {"trixie", "bookworm"},
		config.Ubuntu:   {"noble", "jammy"},
		config.Raspbian: {"bookworm"},
		config.CentOS:   {"9", "10"},
		config.RHEL:     {"9", "10"},
		config.Fedora:   {"44"},
	},
}

// distroOrder is the order distros are listed in.
var distroOrder = []string{
	config.Ubuntu, config.UbuntuPorts, config.Debian, config.Raspbian, config.AlmaLinux, config.Rocky,
	config.CentOSStream, config.CentOS, config.RHEL, config.Fedora, config.EPEL, config.Arch,
}

// index returns the file that shows a mirror of kind carries one release
// of distro, and how old that file may be. Zero means any age: Docker's
// indexes only change when Docker releases.
func index(kind config.MirrorKind, distro, version, arch string) (string, time.Duration) {
	const days = 24 * time.Hour
	if kind == config.MirrorDocker {
		switch distro {
		case config.Debian, config.Ubuntu, config.Raspbian:
			return distro + "/dists/" + version + "/Release", 0
		default:
			return distro + "/" + version + "/" + arch + "/stable/repodata/repomd.xml", 0
		}
	}
	switch distro {
	case config.Ubuntu, config.UbuntuPorts:
		return distro + "/dists/" + version + "-security/Release", 14 * days
	case config.Debian:
		return "debian-security/dists/" + version + "-security/Release", 14 * days
	case config.Raspbian:
		return "raspbian/dists/" + version + "/Release", 30 * days
	case config.AlmaLinux, config.Rocky:
		return distro + "/" + version + "/BaseOS/" + arch + "/os/repodata/repomd.xml", 30 * days
	case config.CentOSStream:
		return "centos-stream/" + version + "-stream/BaseOS/" + arch + "/os/repodata/repomd.xml", 30 * days
	case config.Fedora:
		return "fedora/linux/updates/" + version + "/Everything/" + arch + "/repodata/repomd.xml", 14 * days
	case config.EPEL:
		return "epel/" + version + "/Everything/" + arch + "/repodata/repomd.xml", 14 * days
	case config.Arch:
		return "archlinux/core/os/" + arch + "/core.db", 14 * days
	}
	return "", 0
}

// ProbeDistro checks that the mirror at base carries a fresh copy of one
// release of distro, and returns how old it is.
func ProbeDistro(ctx context.Context, kind config.MirrorKind, base, distro, version, arch string) (time.Duration, error) {
	path, maxAge := index(kind, distro, version, arch)
	if path == "" {
		return 0, fmt.Errorf("no known layout for %s", distro)
	}
	age, err := indexAge(ctx, strings.TrimRight(withScheme(base), "/")+"/"+path)
	if err != nil {
		return 0, err
	}
	if maxAge > 0 && age > maxAge {
		return age, fmt.Errorf("%s is %s old", name(distro, version), days(age))
	}
	return age, nil
}

// Carried probes every distro and reference release a mirror of kind
// might carry and returns the distros it fully covers, plus an error other
// than "not found" if one came up (a mirror that refuses every request).
func Carried(ctx context.Context, kind config.MirrorKind, base string) ([]string, error) {
	refs := RefVersions[kind]
	ok := map[string]bool{}
	var odd error
	var mu sync.Mutex
	var wg sync.WaitGroup
	for distro, versions := range refs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for _, v := range versions {
				if _, err := ProbeDistro(ctx, kind, base, distro, v, "x86_64"); err != nil {
					if !strings.Contains(err.Error(), "HTTP 404") {
						mu.Lock()
						odd = err
						mu.Unlock()
					}
					return
				}
			}
			mu.Lock()
			ok[distro] = true
			mu.Unlock()
		}()
	}
	wg.Wait()
	var out []string
	for _, d := range distroOrder {
		if ok[d] {
			out = append(out, d)
		}
	}
	return out, odd
}

// ProbeMirror checks that url serves what kind needs, not just that it
// answers: OS and Docker mirrors must carry fresh indexes for at least one
// distro, PyPI mirrors must list ansible-core.
func ProbeMirror(ctx context.Context, kind config.MirrorKind, url string) (string, error) {
	switch kind {
	case config.MirrorOS, config.MirrorDocker:
		got, odd := Carried(ctx, kind, url)
		if len(got) == 0 {
			if odd != nil {
				return "", odd
			}
			return "", fmt.Errorf("carries no distro interzone knows, or only stale copies")
		}
		return "carries " + names(got), nil
	case config.MirrorPyPI:
		body, _, err := fetch(ctx, strings.TrimRight(withScheme(url), "/")+"/ansible-core/")
		if err != nil {
			return "", err
		}
		newest := ""
		for _, m := range ansibleFile.FindAllStringSubmatch(body, -1) {
			if newerVersion(m[1], newest) {
				newest = m[1]
			}
		}
		if newest == "" {
			return "", fmt.Errorf("doesn't list ansible-core")
		}
		return "ansible-core " + newest, nil
	}
	return "", fmt.Errorf("unknown mirror kind %q", kind)
}

// MirrorResult is the outcome of probing one known mirror.
type MirrorResult struct {
	Mirror config.Mirror
	OK     bool
	Detail string
	Took   time.Duration
}

// ProbeKnownMirrors checks every mirror in config.KnownMirrors at once.
func ProbeKnownMirrors() []MirrorResult {
	out := make([]MirrorResult, len(config.KnownMirrors))
	var wg sync.WaitGroup
	for i, m := range config.KnownMirrors {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			start := time.Now()
			detail, err := ProbeMirror(ctx, m.Kind, m.URL)
			out[i] = MirrorResult{Mirror: m, OK: err == nil, Detail: detail, Took: time.Since(start)}
			if err != nil {
				out[i].Detail = err.Error()
			}
		}()
	}
	wg.Wait()
	return out
}

// need is one distro release a server installs packages from.
type need struct{ distro, version string }

// serverNeeds maps the server's os-release facts to the repositories it
// uses: OS repositories, and the Docker repository if it installs Docker
// from one (Arch installs it from its own repositories). why is set when
// the OS can't use a mirror at all.
func serverNeeds(facts map[string]string) (os []need, docker *need, why string) {
	ids := strings.Fields(facts["osid"] + " ")
	ver, code, arch := facts["ver"], facts["codename"], facts["arch"]
	major, _, _ := strings.Cut(ver, ".")
	id := ids[0]
	switch id {
	case "ubuntu", "debian", "raspbian", "almalinux", "rocky", "centos", "fedora", "arch", "rhel":
	default:
		// Derivatives such as Linux Mint or CachyOS use their parent's
		// repositories; RHEL rebuilds other than Alma and Rocky don't.
		for _, like := range ids[1:] {
			if like == "ubuntu" || like == "debian" || like == "arch" {
				id = like
				break
			}
		}
	}
	if id == "ubuntu" && facts["ubuntu_codename"] != "" {
		code = facts["ubuntu_codename"]
	}
	switch id {
	case "ubuntu":
		if arch == "x86_64" || arch == "i686" {
			os = []need{{config.Ubuntu, code}}
		} else {
			os = []need{{config.UbuntuPorts, code}}
		}
		docker = &need{config.Ubuntu, code}
	case "debian":
		os, docker = []need{{config.Debian, code}}, &need{config.Debian, code}
	case "raspbian":
		os, docker = []need{{config.Raspbian, code}}, &need{config.Raspbian, code}
	case "almalinux", "rocky":
		os, docker = []need{{id, major}, {config.EPEL, major}}, &need{config.CentOS, major}
	case "centos":
		os, docker = []need{{config.CentOSStream, major}, {config.EPEL, major}}, &need{config.CentOS, major}
	case "fedora":
		os, docker = []need{{config.Fedora, ver}}, &need{config.Fedora, ver}
	case "arch":
		os = []need{{config.Arch, ""}}
	case "rhel":
		docker = &need{config.RHEL, major}
		why = "RHEL's packages come from Red Hat's CDN and can't be mirrored; AlmaLinux or Rocky Linux can"
	default:
		why = "interzone doesn't know where " + orUnknown(facts["os"]) + " keeps its packages"
	}
	return os, docker, why
}

func name(distro, version string) string {
	n := config.DistroNames[distro]
	if n == "" {
		n = distro
	}
	if version != "" {
		n += " " + version
	}
	return n
}

func names(distros []string) string {
	out := make([]string, len(distros))
	for i, d := range distros {
		out[i] = name(d, "")
	}
	return strings.Join(out, ", ")
}

func withScheme(url string) string {
	if !strings.Contains(url, "://") {
		return "https://" + url
	}
	return url
}

func fetch(ctx context.Context, url string) (string, http.Header, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", nil, err
	}
	// Some Nexus mirrors answer 406 without an Accept header.
	req.Header.Set("Accept", "text/html, */*")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return "", nil, fmt.Errorf("timed out")
		}
		return "", nil, fmt.Errorf("unreachable")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", nil, fmt.Errorf("HTTP %d for %s", resp.StatusCode, url)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return "", nil, fmt.Errorf("download cut off")
	}
	return string(b), resp.Header, nil
}

// indexAge returns how long ago a repository index was generated: an APT
// Release file's Date, a repomd.xml's revision, or else Last-Modified.
func indexAge(ctx context.Context, url string) (time.Duration, error) {
	body, hdr, err := fetch(ctx, url)
	if err != nil {
		return 0, err
	}
	var t time.Time
	switch {
	case strings.HasSuffix(url, "/Release"):
		m := releaseDate.FindStringSubmatch(body)
		if m == nil {
			return 0, fmt.Errorf("not an APT Release file")
		}
		if t, err = parseDate(m[1]); err != nil {
			return 0, err
		}
	case strings.HasSuffix(url, "/repomd.xml"):
		m := repomdRev.FindStringSubmatch(body)
		if m == nil {
			return 0, fmt.Errorf("not a dnf repository index")
		}
		sec, _ := strconv.ParseInt(m[1], 10, 64)
		t = time.Unix(sec, 0)
	default:
		if t, err = parseDate(hdr.Get("Last-Modified")); err != nil {
			return 0, err
		}
	}
	return max(0, time.Since(t)), nil
}

func parseDate(s string) (time.Time, error) {
	s = strings.TrimSpace(s)
	for _, layout := range []string{time.RFC1123Z, time.RFC1123, "Mon, 2 Jan 2006 15:04:05 MST", "Mon, 2 Jan 2006 15:04:05 -0700"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("unreadable date %q", s)
}

func days(d time.Duration) string {
	switch n := int(d.Hours() / 24); n {
	case 0:
		return "<1 day"
	case 1:
		return "1 day"
	default:
		return fmt.Sprintf("%d days", n)
	}
}

// newerVersion reports whether dotted version a is newer than b.
func newerVersion(a, b string) bool {
	if b == "" {
		return true
	}
	pa, pb := strings.Split(a, "."), strings.Split(b, ".")
	for i := 0; i < len(pa) && i < len(pb); i++ {
		x, _ := strconv.Atoi(pa[i])
		y, _ := strconv.Atoi(pb[i])
		if x != y {
			return x > y
		}
	}
	return len(pa) > len(pb)
}
