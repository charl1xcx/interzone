// Package ipfs fetches what a deploy needs from interzone's IPFS swarm.
package ipfs

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
)

// Registry serves an OCI image layout that is reachable over HTTP (an IPFS
// gateway's /ipfs/<root>/oci) as a read-only Docker registry, so the
// playbook pulls <listen address>/<path>:<tag> the way it pulls from zot.
type Registry struct {
	Base   string // URL of the layout's directory, without a trailing slash
	Client *http.Client
	refs   map[string]descriptor // "element-hq/synapse:v1.2" -> its index
}

type descriptor struct {
	MediaType string `json:"mediaType"`
	Digest    string `json:"digest"`
	Size      int64  `json:"size"`
}

var digestRe = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)

// Load reads the layout's index.json.
func (r *Registry) Load() error {
	var idx struct {
		Manifests []struct {
			descriptor
			Annotations map[string]string `json:"annotations"`
		} `json:"manifests"`
	}
	b, err := r.get("index.json", 4<<20)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(b, &idx); err != nil {
		return fmt.Errorf("index.json: %w", err)
	}
	r.refs = map[string]descriptor{}
	for _, m := range idx.Manifests {
		if name := m.Annotations["org.opencontainers.image.ref.name"]; name != "" && digestRe.MatchString(m.Digest) {
			r.refs[name] = m.descriptor
		}
	}
	return nil
}

func (r *Registry) get(path string, limit int64) ([]byte, error) {
	res, err := r.Client.Get(r.Base + "/" + path)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s: %s", path, res.Status)
	}
	return io.ReadAll(io.LimitReader(res.Body, limit))
}

func blobPath(digest string) string { return "blobs/sha256/" + strings.TrimPrefix(digest, "sha256:") }

func (r *Registry) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodGet && req.Method != http.MethodHead {
		http.Error(w, "read-only", http.StatusMethodNotAllowed)
		return
	}
	p := req.URL.Path
	if p == "/v2/" || p == "/v2" {
		w.Header().Set("Docker-Distribution-API-Version", "registry/2.0")
		return
	}
	if i := strings.LastIndex(p, "/manifests/"); strings.HasPrefix(p, "/v2/") && i > 3 {
		r.manifest(w, req, p[4:i], p[i+len("/manifests/"):])
		return
	}
	if i := strings.LastIndex(p, "/blobs/"); strings.HasPrefix(p, "/v2/") && i > 3 {
		r.blob(w, req, p[i+len("/blobs/"):])
		return
	}
	http.NotFound(w, req)
}

func (r *Registry) manifest(w http.ResponseWriter, req *http.Request, name, ref string) {
	d, ok := r.refs[name+":"+ref]
	if digestRe.MatchString(ref) {
		d, ok = descriptor{Digest: ref}, true
	}
	if !ok {
		http.Error(w, `{"errors":[{"code":"MANIFEST_UNKNOWN"}]}`, http.StatusNotFound)
		return
	}
	b, err := r.get(blobPath(d.Digest), 4<<20)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	// The digest comes from the signed index; the bytes from whoever had them.
	if sum := sha256.Sum256(b); "sha256:"+hex.EncodeToString(sum[:]) != d.Digest {
		http.Error(w, "digest mismatch", http.StatusBadGateway)
		return
	}
	if d.MediaType == "" {
		var m struct {
			MediaType string `json:"mediaType"`
		}
		_ = json.Unmarshal(b, &m)
		d.MediaType = m.MediaType
	}
	w.Header().Set("Content-Type", d.MediaType)
	w.Header().Set("Docker-Content-Digest", d.Digest)
	w.Header().Set("Content-Length", strconv.Itoa(len(b)))
	if req.Method == http.MethodGet {
		_, _ = w.Write(b)
	}
}

func (r *Registry) blob(w http.ResponseWriter, req *http.Request, digest string) {
	if !digestRe.MatchString(digest) {
		http.NotFound(w, req)
		return
	}
	up, err := http.NewRequestWithContext(req.Context(), req.Method, r.Base+"/"+blobPath(digest), nil)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	res, err := r.Client.Do(up)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		http.Error(w, res.Status, http.StatusNotFound)
		return
	}
	// Docker checks the blob against its digest itself.
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Docker-Content-Digest", digest)
	if res.ContentLength >= 0 {
		w.Header().Set("Content-Length", strconv.FormatInt(res.ContentLength, 10))
	}
	_, _ = io.Copy(w, res.Body)
}
