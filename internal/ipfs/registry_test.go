package ipfs

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func writeBlob(t *testing.T, dir string, b []byte) string {
	sum := sha256.Sum256(b)
	h := hex.EncodeToString(sum[:])
	if err := os.WriteFile(filepath.Join(dir, "blobs", "sha256", h), b, 0o644); err != nil {
		t.Fatal(err)
	}
	return "sha256:" + h
}

func TestRegistry(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "blobs", "sha256"), 0o755)
	layer := writeBlob(t, dir, []byte("layer bytes"))
	man := []byte(fmt.Sprintf(`{"mediaType":"application/vnd.oci.image.manifest.v1+json","layers":[{"digest":%q}]}`, layer))
	md := writeBlob(t, dir, man)
	bad := "sha256:" + fmt.Sprintf("%064x", 1)
	os.WriteFile(filepath.Join(dir, "blobs", "sha256", fmt.Sprintf("%064x", 1)), man, 0o644) // not what its name says
	os.WriteFile(filepath.Join(dir, "index.json"), []byte(fmt.Sprintf(`{"manifests":[
		{"mediaType":"application/vnd.oci.image.manifest.v1+json","digest":%q,"size":%d,"annotations":{"org.opencontainers.image.ref.name":"element-hq/synapse:v1"}},
		{"digest":%q,"annotations":{"org.opencontainers.image.ref.name":"evil/tampered:v1"}}]}`, md, len(man), bad)), 0o644)
	gw := httptest.NewServer(http.FileServer(http.Dir(dir)))
	defer gw.Close()

	r := &Registry{Base: gw.URL, Client: gw.Client()}
	if err := r.Load(); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(r)
	defer srv.Close()
	get := func(method, path string) (*http.Response, string) {
		req, _ := http.NewRequest(method, srv.URL+path, nil)
		res, err := srv.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(res.Body)
		res.Body.Close()
		return res, string(b)
	}

	for _, ref := range []string{"v1", md} {
		res, body := get("GET", "/v2/element-hq/synapse/manifests/"+ref)
		if res.StatusCode != 200 || body != string(man) ||
			res.Header.Get("Content-Type") != "application/vnd.oci.image.manifest.v1+json" || res.Header.Get("Docker-Content-Digest") != md {
			t.Errorf("manifest %s: %d %q %v", ref, res.StatusCode, body, res.Header)
		}
	}
	if res, body := get("HEAD", "/v2/element-hq/synapse/manifests/v1"); res.StatusCode != 200 || body != "" || res.ContentLength != int64(len(man)) {
		t.Errorf("HEAD: %d %q %d", res.StatusCode, body, res.ContentLength)
	}
	if res, body := get("GET", "/v2/element-hq/synapse/blobs/"+layer); res.StatusCode != 200 || body != "layer bytes" {
		t.Errorf("blob: %d %q", res.StatusCode, body)
	}
	for path, want := range map[string]int{
		"/v2/":                                200,
		"/v2/element-hq/synapse/manifests/v2": 404,
		"/v2/evil/tampered/manifests/v1":      502, // bytes that don't match the index
		"/v2/x/blobs/sha256:../../index.json": 404,
	} {
		if res, _ := get("GET", path); res.StatusCode != want {
			t.Errorf("%s: %d, want %d", path, res.StatusCode, want)
		}
	}
	if res, _ := get("PUT", "/v2/element-hq/synapse/manifests/v1"); res.StatusCode != 405 {
		t.Errorf("PUT: %d", res.StatusCode)
	}
}
