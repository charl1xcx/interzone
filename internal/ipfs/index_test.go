package ipfs

import (
	"crypto/ed25519"
	"encoding/base64"
	"strings"
	"testing"
)

func TestVerify(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(nil)
	defer func(k []byte) { publicKey = k }(publicKey)
	publicKey = pub
	cid := "b" + strings.Repeat("a", 58)
	good := `{"version":2,"kubo":"v0.43.1","interzone":"v0.5.33",
		"playbook":{"repo":"spantaleev/matrix-docker-ansible-deploy","commit":"` + strings.Repeat("ab", 20) + `"},
		"parts":{"packages":"` + cid + `","oci":"` + cid + `","git":"` + cid + `","bin":"` + cid + `"},
		"bin":{"ipfs-linux-amd64":"` + strings.Repeat("0", 64) + `"}}`
	sign := func(s string) []byte {
		return []byte(base64.StdEncoding.EncodeToString(ed25519.Sign(priv, []byte(s))) + "\n")
	}

	x, err := Verify([]byte(good), sign(good))
	if err != nil || x.Version != 2 || x.Parts.OCI != cid {
		t.Fatalf("good index: %v %+v", err, x)
	}
	if _, err := Verify([]byte(good+" "), sign(good)); err == nil {
		t.Error("a changed index passed")
	}
	if _, err := Verify([]byte(good), []byte("not base64")); err == nil {
		t.Error("a bad signature passed")
	}
	for _, bad := range []string{
		strings.Replace(good, `"oci":"`+cid, `"oci":"`+cid+`/../x`, 1),
		strings.Replace(good, `"kubo":"v0.43.1"`, `"kubo":"v0.43.1; rm -rf /"`, 1),
		strings.Replace(good, `"ipfs-linux-amd64"`, `"../ipfs"`, 1),
	} {
		if _, err := Verify([]byte(bad), sign(bad)); err == nil {
			t.Errorf("signed but malformed index passed: %.80s", bad)
		}
	}
}
