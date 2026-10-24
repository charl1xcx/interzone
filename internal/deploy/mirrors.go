package deploy

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"

	"interzone/internal/config"
)

// maxIndexAge is how old a mirror's security index may be before the
// mirror counts as stale. Security suites change every few days upstream.
const maxIndexAge = 14 * 24 * time.Hour

var (
	releaseDate = regexp.MustCompile(`(?m)^Date:\s*(.+)$`)
	ansibleFile = regexp.MustCompile(`ansible[_-]core-(\d+\.\d+\.\d+)`)
)

// MirrorResult is the outcome of probing one known mirror.
type MirrorResult struct {
	Mirror config.Mirror
	OK     bool
	Detail string
	Took   time.Duration
}

// ProbeMirror checks that url serves what kind needs, not just that it
// answers: APT and Docker mirrors must have a recent Release file, PyPI
// mirrors must list ansible-core.
func ProbeMirror(ctx context.Context, kind config.MirrorKind, url string) (string, error) {
	base := strings.TrimRight(url, "/")
	if !strings.Contains(base, "://") {
		base = "https://" + base
	}
	switch kind {
	case config.MirrorAPT:
		var parts []string
		for _, s := range []struct{ distro, path string }{
			{"Ubuntu", "/ubuntu/dists/noble-security/Release"},
			{"Debian", "/debian-security/dists/trixie-security/Release"},
		} {
			age, err := releaseAge(ctx, base+s.path)
			if err != nil {
				return "", fmt.Errorf("%s: %w", s.distro, err)
			}
			if age > maxIndexAge {
				return "", fmt.Errorf("%s security updates are %s old", s.distro, days(age))
			}
			parts = append(parts, s.distro+" "+days(age)+" old")
		}
		return strings.Join(parts, ", "), nil
	case config.MirrorDocker:
		age, err := releaseAge(ctx, base+"/ubuntu/dists/noble/Release")
		if err != nil {
			return "", err
		}
		return "updated " + days(age) + " ago", nil
	case config.MirrorPyPI:
		body, err := fetch(ctx, base+"/ansible-core/")
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

// ProbeKnownMirrors checks every mirror in config.KnownMirrors at once.
func ProbeKnownMirrors() []MirrorResult {
	out := make([]MirrorResult, len(config.KnownMirrors))
	var wg sync.WaitGroup
	for i, m := range config.KnownMirrors {
		wg.Add(1)
		go func(i int, m config.Mirror) {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			start := time.Now()
			detail, err := ProbeMirror(ctx, m.Kind, m.URL)
			out[i] = MirrorResult{Mirror: m, OK: err == nil, Detail: detail, Took: time.Since(start)}
			if err != nil {
				out[i].Detail = err.Error()
			}
		}(i, m)
	}
	wg.Wait()
	return out
}

func fetch(ctx context.Context, url string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	// Some Nexus mirrors answer 406 without an Accept header.
	req.Header.Set("Accept", "text/html, */*")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return "", fmt.Errorf("timed out")
		}
		return "", fmt.Errorf("unreachable")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("HTTP %d for %s", resp.StatusCode, url)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return "", fmt.Errorf("download cut off")
	}
	return string(b), nil
}

// releaseAge returns how long ago an APT Release file was generated.
func releaseAge(ctx context.Context, url string) (time.Duration, error) {
	body, err := fetch(ctx, url)
	if err != nil {
		return 0, err
	}
	m := releaseDate.FindStringSubmatch(body)
	if m == nil {
		return 0, fmt.Errorf("not an APT Release file")
	}
	s := strings.TrimSpace(m[1])
	for _, layout := range []string{time.RFC1123Z, time.RFC1123, "Mon, 2 Jan 2006 15:04:05 MST", "Mon, 2 Jan 2006 15:04:05 -0700"} {
		if t, err := time.Parse(layout, s); err == nil {
			return max(0, time.Since(t)), nil
		}
	}
	return 0, fmt.Errorf("unreadable date %q", s)
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
		var x, y int
		fmt.Sscan(pa[i], &x)
		fmt.Sscan(pb[i], &y)
		if x != y {
			return x > y
		}
	}
	return len(pa) > len(pb)
}
