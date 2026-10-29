package ipfs

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
)

// fakeNode answers the few RPC calls Follow makes, from one signed index.
type fakeNode struct {
	mu            sync.Mutex
	root          string
	index, sig    string
	pinned, unpin []string
}

func (n *fakeNode) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	n.mu.Lock()
	defer n.mu.Unlock()
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", 405)
		return
	}
	arg := r.URL.Query().Get("arg")
	switch strings.TrimPrefix(r.URL.Path, "/api/v0/") {
	case "name/resolve":
		w.Write([]byte(`{"Path":"/ipfs/` + n.root + `"}`))
	case "cat":
		switch arg {
		case "/ipfs/" + n.root + "/index.json":
			w.Write([]byte(n.index))
		case "/ipfs/" + n.root + "/index.json.sig":
			w.Write([]byte(n.sig))
		default:
			http.Error(w, `{"Message":"no link"}`, 500)
		}
	case "pin/add":
		n.pinned = append(n.pinned, arg)
	case "pin/rm":
		n.unpin = append(n.unpin, arg)
	case "repo/gc":
	default:
		http.Error(w, "unexpected", 404)
	}
}

func TestFollow(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(nil)
	defer func(k []byte) { publicKey = k }(publicKey)
	publicKey = pub
	cid := func(c string) string { return "b" + strings.Repeat(c, 58) }
	index := func(version, c string) (string, string) {
		s := `{"version":` + version + `,"kubo":"v0.43.1","interzone":"v0.5.34",
			"playbook":{"repo":"spantaleev/matrix-docker-ansible-deploy","commit":"` + strings.Repeat("ab", 20) + `"},
			"parts":{"packages":"` + cid(c) + `","oci":"` + cid("o") + `","git":"` + cid(c) + `","bin":"` + cid(c) + `"}}`
		return s, base64.StdEncoding.EncodeToString(ed25519.Sign(priv, []byte(s)))
	}
	node := &fakeNode{root: cid("r")}
	node.index, node.sig = index("5", "p")
	srv := httptest.NewServer(node)
	defer srv.Close()
	state := filepath.Join(t.TempDir(), "follow.json")
	ctx, log := context.Background(), func(string) {}

	if err := Follow(ctx, srv.URL, "images", state, log); err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(node.pinned, cid("o")) || slices.Contains(node.pinned, cid("p")) {
		t.Errorf("images only pinned %v", node.pinned)
	}

	// A newer publish: everything pinned, the old root let go.
	node.root, node.pinned = cid("s"), nil
	node.index, node.sig = index("6", "q")
	if err := Follow(ctx, srv.URL, "all", state, log); err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(node.pinned, cid("q")) || !slices.Contains(node.unpin, cid("r")) || slices.Contains(node.unpin, cid("o")) {
		t.Errorf("pinned %v, unpinned %v", node.pinned, node.unpin)
	}

	// An older one, or one not signed by the mirror, is refused.
	node.root, node.pinned = cid("t"), nil
	node.index, node.sig = index("4", "x")
	if err := Follow(ctx, srv.URL, "all", state, log); err == nil || len(node.pinned) > 0 {
		t.Errorf("an older publish was taken: %v %v", err, node.pinned)
	}
	node.index, node.sig = index("7", "x")
	node.sig = base64.StdEncoding.EncodeToString(make([]byte, 64))
	if err := Follow(ctx, srv.URL, "all", state, log); err == nil || len(node.pinned) > 0 {
		t.Errorf("an unsigned publish was taken: %v %v", err, node.pinned)
	}
}
