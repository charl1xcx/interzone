package ipfs

import (
	"crypto/ed25519"
	_ "embed"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// SwarmKey admits a node to interzone's private swarm. It ships with
// interzone, so it isn't secret: it keeps the public IPFS network, and
// whoever crawls it, from seeing which servers hold what.
//
//go:embed swarm.key
var SwarmKey string

// Bootstrap are the swarm's entry nodes (IPFS.md in interzone-mirror).
var Bootstrap = []string{
	"/dns4/infamous.g0th.ir/tcp/4001/p2p/12D3KooWPCRCpBuQErPEwg6Z6xTFARK15ae8vnkmsSS95EfrHduz",
	"/dns4/yeule.g0th.ir/tcp/443/wss/p2p/12D3KooWPCRCpBuQErPEwg6Z6xTFARK15ae8vnkmsSS95EfrHduz",
}

const (
	// Name is the IPNS name the newest root is published under.
	Name = "k51qzi5uqu5dj8u2m9w83ce9q9yoomao9tilqz5hrm99oxnnsmflyyldrtaz3n"
	// IndexURL serves the newest index.json (and .sig) over HTTPS too.
	IndexURL = "https://infamous.g0th.ir/.ipfs/"
	// KuboVersion is the IPFS daemon to install when no index says otherwise.
	KuboVersion = "v0.43.1"
)

// publicKey checks index.json's signature (secrets/ipfs-sign.pem in
// interzone-mirror holds its private half).
var publicKey, _ = base64.StdEncoding.DecodeString("vMZTAm9Xt1p1rgjUvHuxz/Jv94J4yxy9PXZMpWfz2Qk=")

// Index says what one publish holds. Its parts are IPFS directories, so
// once the index is verified everything under them is too: IPFS checks
// each block against its CID.
type Index struct {
	Version   int64  `json:"version"`
	Created   string `json:"created"`
	Interzone string `json:"interzone"`
	Kubo      string `json:"kubo"`
	Playbook  struct {
		Repo   string `json:"repo"`
		Commit string `json:"commit"`
	} `json:"playbook"`
	Parts struct {
		Packages string `json:"packages"`
		OCI      string `json:"oci"`
		Git      string `json:"git"`
		Bin      string `json:"bin"`
	} `json:"parts"`
	// Bin holds the SHA-256 of each file in the bin part, so a copy
	// fetched over HTTPS can be checked too.
	Bin map[string]string `json:"bin"`
}

var (
	cidRe     = regexp.MustCompile(`^b[a-z2-7]{50,100}$`) // CIDv1, base32
	commitRe  = regexp.MustCompile(`^[0-9a-f]{40}$`)
	versionRe = regexp.MustCompile(`^v\d+\.\d+\.\d+$`)
	repoRe    = regexp.MustCompile(`^[A-Za-z0-9._-]+/[A-Za-z0-9._-]+$`)
	sha256Re  = regexp.MustCompile(`^[0-9a-f]{64}$`)
)

// Verify checks index.json against its signature and returns it. Every
// value that ends up in a URL or a command is checked for its shape too.
func Verify(index, sig []byte) (*Index, error) {
	s, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(sig)))
	if err != nil || !ed25519.Verify(ed25519.PublicKey(publicKey), index, s) {
		return nil, errors.New("index.json isn't signed by interzone-mirror's key")
	}
	var x Index
	if err := json.Unmarshal(index, &x); err != nil {
		return nil, fmt.Errorf("index.json: %w", err)
	}
	p := x.Parts
	for _, cid := range []string{p.Packages, p.OCI, p.Git, p.Bin} {
		if !cidRe.MatchString(cid) {
			return nil, fmt.Errorf("index.json: %q isn't a CID", cid)
		}
	}
	if !commitRe.MatchString(x.Playbook.Commit) || !repoRe.MatchString(x.Playbook.Repo) ||
		!versionRe.MatchString(x.Kubo) || !versionRe.MatchString(x.Interzone) {
		return nil, errors.New("index.json: a malformed playbook, kubo or interzone version")
	}
	for name, sum := range x.Bin {
		if !sha256Re.MatchString(sum) || strings.ContainsAny(name, "/\\") {
			return nil, fmt.Errorf("index.json: bad bin entry %q", name)
		}
	}
	return &x, nil
}
