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
	"strings"
	"time"
)

// Follow keeps a hosting node's copy of the newest publish: it resolves
// the IPNS name on the node at api, checks the index's signature, pins
// what parts asks for ("all", or "images": the OCI layout) with the index
// itself, then unpins what the last run pinned and collects it. state is
// a file remembering that, and the newest version seen, so an older
// publish is never taken over a newer one.
func Follow(ctx context.Context, api, parts, state string, log func(string)) error {
	c := apiClient{base: strings.TrimRight(api, "/") + "/api/v0/"}
	var last struct {
		Version int64    `json:"version"`
		Pins    []string `json:"pins"`
	}
	if b, err := os.ReadFile(state); err == nil {
		_ = json.Unmarshal(b, &last)
	}

	var res struct{ Path string }
	if err := c.call(ctx, &res, "name/resolve", "arg", "/ipns/"+Name, "recursive", "true", "timeout", "5m"); err != nil {
		return fmt.Errorf("resolving the publish: %w", err)
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
	switch parts {
	case "all":
		pins = append(pins, idx.Parts.Packages, idx.Parts.OCI, idx.Parts.Git, idx.Parts.Bin)
	case "images":
		pins = append(pins, idx.Parts.OCI)
	default:
		return fmt.Errorf("parts must be all or images, not %q", parts)
	}
	log(fmt.Sprintf("hosting the publish of %s (%s)", idx.Created, parts))
	if err := c.call(ctx, nil, "pin/add", "arg", root, "recursive", "false"); err != nil {
		return fmt.Errorf("pinning %s: %w", root, err)
	}
	for _, p := range pins {
		log("fetching " + p)
		if err := c.call(ctx, nil, "pin/add", "arg", p); err != nil {
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
