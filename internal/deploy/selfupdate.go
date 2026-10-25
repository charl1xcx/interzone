package deploy

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// ReleasesAPI is where interzone looks for new versions: a Forgejo
// repository inside the country, reachable during a blackout.
var ReleasesAPI = "https://itsjustadream.g0th.ir/api/v1/repos/vvvomit/interzone/releases"

// Release is a published interzone version.
type Release struct {
	Tag    string `json:"tag_name"`
	Name   string `json:"name"`
	Body   string `json:"body"`
	URL    string `json:"html_url"`
	Assets []struct {
		Name string `json:"name"`
		URL  string `json:"browser_download_url"`
	} `json:"assets"`
}

func (rel Release) asset(name string) string {
	for _, a := range rel.Assets {
		if a.Name == name {
			return a.URL
		}
	}
	return ""
}

var updateClient = &http.Client{Timeout: 10 * time.Minute}

// LatestRelease returns the newest published release.
func LatestRelease(ctx context.Context) (*Release, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, ReleasesAPI+"/latest", nil)
	if err != nil {
		return nil, err
	}
	resp, err := updateClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("can't reach the release server: %s", shortNetErr(err))
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("the release server answered %s", resp.Status)
	}
	var rel Release
	if err := json.NewDecoder(resp.Body).Decode(&rel); err != nil {
		return nil, err
	}
	return &rel, nil
}

// Newer reports whether version a is newer than b ("v0.4.1" > "v0.4.0").
// Anything that isn't a version, such as "dev", is older than everything.
func Newer(a, b string) bool {
	pa, oka := parseVersion(a)
	pb, okb := parseVersion(b)
	if !oka {
		return false
	}
	if !okb {
		return true
	}
	for i := range pa {
		if pa[i] != pb[i] {
			return pa[i] > pb[i]
		}
	}
	return false
}

func parseVersion(v string) ([3]int, bool) {
	var out [3]int
	v, _, _ = strings.Cut(strings.TrimPrefix(v, "v"), "-")
	parts := strings.Split(v, ".")
	if len(parts) != 3 {
		return out, false
	}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil {
			return out, false
		}
		out[i] = n
	}
	return out, true
}

func (r *Runner) selfUpdate(ctx context.Context) error {
	r.begin(StepSelfUpdate)
	rel, err := LatestRelease(ctx)
	if err != nil {
		return err
	}
	r.note(StepSelfUpdate, "latest release: "+rel.Tag+"; this is "+Version)
	if !Newer(rel.Tag, Version) && !r.opts.Force {
		r.note(StepSelfUpdate, "interzone is up to date")
		r.result.Version = Version
		return nil
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	if exe, err = filepath.EvalSymlinks(exe); err != nil {
		return err
	}
	name := "interzone-" + runtime.GOOS + "-" + runtime.GOARCH
	binURL, sumsURL := rel.asset(name), rel.asset("SHA256SUMS")
	if binURL == "" {
		return fmt.Errorf("release %s has no %s download", rel.Tag, name)
	}
	if sumsURL == "" {
		return fmt.Errorf("release %s has no SHA256SUMS, so the download can't be verified", rel.Tag)
	}
	sums, err := download(ctx, sumsURL)
	if err != nil {
		return fmt.Errorf("SHA256SUMS: %w", err)
	}
	want := checksumFor(string(sums), name)
	if want == "" {
		return fmt.Errorf("SHA256SUMS doesn't list %s", name)
	}

	r.note(StepSelfUpdate, "downloading "+name+" "+rel.Tag+"…")
	tmp, err := os.CreateTemp(filepath.Dir(exe), ".interzone-update-*")
	if err != nil {
		return fmt.Errorf("can't write next to %s: %w", exe, err)
	}
	defer os.Remove(tmp.Name())
	got, err := downloadTo(ctx, binURL, tmp)
	tmp.Close()
	if err != nil {
		return err
	}
	if got != want {
		return fmt.Errorf("checksum mismatch: SHA256SUMS says %s, the download is %s; nothing was changed", want, got)
	}
	r.note(StepSelfUpdate, "checksum verified: "+got)
	if err := os.Chmod(tmp.Name(), 0o755); err != nil {
		return err
	}
	if err := os.Rename(tmp.Name(), exe); err != nil {
		return fmt.Errorf("couldn't replace %s: %w", exe, err)
	}
	r.note(StepSelfUpdate, "installed "+rel.Tag+" at "+exe)
	r.result.Version = rel.Tag
	r.result.File = exe
	return nil
}

// checksumFor finds name's hash in a sha256sum listing.
func checksumFor(sums, name string) string {
	sc := bufio.NewScanner(strings.NewReader(sums))
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) == 2 && strings.TrimPrefix(f[1], "*") == name {
			return strings.ToLower(f[0])
		}
	}
	return ""
}

func download(ctx context.Context, url string) ([]byte, error) {
	resp, err := get(ctx, url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	return io.ReadAll(io.LimitReader(resp.Body, 1<<20))
}

// downloadTo writes url to w and returns the SHA-256 of what it wrote.
func downloadTo(ctx context.Context, url string, w io.Writer) (string, error) {
	resp, err := get(ctx, url)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	h := sha256.New()
	if _, err := io.Copy(io.MultiWriter(w, h), resp.Body); err != nil {
		return "", fmt.Errorf("download interrupted: %w", err)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func get(ctx context.Context, url string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := updateClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("can't download %s: %s", filepath.Base(url), shortNetErr(err))
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, fmt.Errorf("downloading %s: %s", filepath.Base(url), resp.Status)
	}
	return resp, nil
}
