package ipfs

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

// PartNames are the parts of a publish, in the order they're fetched.
var PartNames = []string{"oci", "packages", "git", "bin"}

// Parts reads which parts to host: names from PartNames, comma-separated,
// or "all", or "images" (oci).
func Parts(spec string) ([]string, error) {
	switch spec {
	case "all":
		return PartNames, nil
	case "images":
		return []string{"oci"}, nil
	}
	var names []string
	for _, n := range strings.Split(spec, ",") {
		if !slices.Contains(PartNames, n) {
			return nil, fmt.Errorf("parts are all, images, or some of %s, not %q", strings.Join(PartNames, ","), spec)
		}
		if !slices.Contains(names, n) {
			names = append(names, n)
		}
	}
	return names, nil
}

// resolveTries and resolveWait are how often, and how far apart, Follow
// looks the name up before giving up until the next run.
var resolveTries, resolveWait = 10, 30 * time.Second

// stall is how long a fetch may go without a new block before Follow gives
// up, so a publish the swarm can't serve whole doesn't hold the node forever.
var stall = 20 * time.Minute

// Follow keeps a hosting node's copy of the newest publish: it resolves
// the IPNS name on the node at api, checks the index's signature, pins
// the parts asked for (see Parts) with the index itself, then unpins what
// the last run pinned and collects it. state is a file remembering that,
// and the newest version seen, so an older publish is never taken over a
// newer one.
func Follow(ctx context.Context, api, parts, state string, log func(string)) error {
	c := apiClient{base: strings.TrimRight(api, "/") + "/api/v0/"}
	names, err := Parts(parts)
	if err != nil {
		return err
	}
	var last struct {
		Version int64    `json:"version"`
		Pins    []string `json:"pins"`
	}
	if b, err := os.ReadFile(state); err == nil {
		_ = json.Unmarshal(b, &last)
	}

	// A node that just started has no peers yet and fails at once; retry.
	var res struct{ Path string }
	for try := 1; ; try++ {
		err := c.call(ctx, &res, "name/resolve", "arg", "/ipns/"+Name, "recursive", "true", "timeout", "5m")
		if err == nil {
			break
		}
		if try == resolveTries {
			return fmt.Errorf("resolving the publish: %w", err)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(resolveWait):
		}
	}
	root, ok := strings.CutPrefix(res.Path, "/ipfs/")
	if !ok || !cidRe.MatchString(root) {
		return fmt.Errorf("the name resolved to %q", res.Path)
	}
	sig, err := c.cat(ctx, root+"/index.json.sig", 4096)
	if err != nil {
		return err
	}
	index, err := c.cat(ctx, root+"/index.json", 1<<20)
	if err != nil {
		return err
	}
	idx, err := Verify(index, sig)
	if err != nil {
		return err
	}
	if idx.Version < last.Version {
		return fmt.Errorf("the publish found (%s) is older than one already hosted; keeping that", idx.Created)
	}

	// The root's own block and the index, so peers can find their way in.
	pins := []string{root + "/index.json", root + "/index.json.sig"}
	cids := map[string]string{"packages": idx.Parts.Packages, "oci": idx.Parts.OCI, "git": idx.Parts.Git, "bin": idx.Parts.Bin}
	for _, n := range names {
		pins = append(pins, cids[n])
	}
	log(fmt.Sprintf("hosting the publish of %s (%s)", idx.Created, strings.Join(names, ", ")))
	if err := c.call(ctx, nil, "pin/add", "arg", root, "recursive", "false"); err != nil {
		return fmt.Errorf("pinning %s: %w", root, err)
	}
	for _, p := range pins {
		log("fetching " + p)
		if err := c.pin(ctx, p); err != nil {
			return fmt.Errorf("pinning %s: %w", p, err)
		}
	}
	pins = append(pins, root)

	keep := map[string]bool{}
	for _, p := range pins {
		keep[p] = true
	}
	for _, p := range last.Pins {
		if !keep[p] {
			_ = c.call(ctx, nil, "pin/rm", "arg", p) // already gone is fine
		}
	}
	b, _ := json.Marshal(map[string]any{"version": idx.Version, "pins": pins})
	if err := os.MkdirAll(filepath.Dir(state), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(state, b, 0o644); err != nil {
		return err
	}
	if len(last.Pins) > 0 {
		_ = c.call(ctx, nil, "repo/gc", "quiet", "true")
	}
	log("up to date")
	return nil
}

// apiClient talks to a kubo daemon's RPC API (POST only).
type apiClient struct{ base string }

func (c apiClient) do(ctx context.Context, cmd string, args ...string) (*http.Response, error) {
	q := url.Values{}
	for i := 0; i+1 < len(args); i += 2 {
		q.Add(args[i], args[i+1])
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+cmd+"?"+q.Encode(), nil)
	if err != nil {
		return nil, err
	}
	res, err := (&http.Client{Transport: &http.Transport{Proxy: nil}}).Do(req) // the node is local; never a proxy
	if err != nil {
		return nil, err
	}
	if res.StatusCode != http.StatusOK {
		defer res.Body.Close()
		var e struct{ Message string }
		b, _ := io.ReadAll(io.LimitReader(res.Body, 4096))
		if json.Unmarshal(b, &e) == nil && e.Message != "" {
			return nil, fmt.Errorf("%s", e.Message)
		}
		return nil, fmt.Errorf("%s: %s", cmd, res.Status)
	}
	return res, nil
}

func (c apiClient) call(ctx context.Context, out any, cmd string, args ...string) error {
	res, err := c.do(ctx, cmd, args...)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if out == nil {
		_, err = io.Copy(io.Discard, res.Body) // pin/add answers when it's done
		return err
	}
	return json.NewDecoder(res.Body).Decode(out)
}

// pin pins path recursively, giving up once no block has arrived for stall.
func (c apiClient) pin(ctx context.Context, path string) error {
	ctx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	stalled := fmt.Errorf("nothing arrived for %s; the swarm doesn't have all of it now", stall)
	t := time.AfterFunc(stall, func() { cancel(stalled) })
	defer t.Stop()
	res, err := c.do(ctx, "pin/add", "arg", path, "progress", "true")
	if err != nil {
		if context.Cause(ctx) == stalled {
			return stalled
		}
		return err
	}
	defer res.Body.Close()
	dec, last := json.NewDecoder(res.Body), -1
	for {
		var m struct{ Progress int }
		err := dec.Decode(&m)
		switch {
		case err == io.EOF:
			if e := res.Trailer.Get("X-Stream-Error"); e != "" {
				return fmt.Errorf("%s", e)
			}
			return nil
		case err != nil && context.Cause(ctx) == stalled:
			return stalled
		case err != nil:
			return err
		}
		if m.Progress != last {
			last = m.Progress
			t.Reset(stall)
		}
	}
}

func (c apiClient) cat(ctx context.Context, path string, limit int64) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	res, err := c.do(ctx, "cat", "arg", "/ipfs/"+path)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}
	defer res.Body.Close()
	return io.ReadAll(io.LimitReader(res.Body, limit))
}
