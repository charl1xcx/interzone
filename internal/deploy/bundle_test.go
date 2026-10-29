package deploy

import (
	"archive/tar"
	"compress/gzip"
	"os"
	"path/filepath"
	"testing"
)

func writeTar(t *testing.T, entries []tar.Header) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "b.tar.gz")
	f, err := os.Create(p)
	if err != nil {
		t.Fatal(err)
	}
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)
	for _, h := range entries {
		if h.Typeflag == tar.TypeReg {
			h.Size = int64(len("x"))
		}
		if err := tw.WriteHeader(&h); err != nil {
			t.Fatal(err)
		}
		if h.Typeflag == tar.TypeReg {
			_, _ = tw.Write([]byte("x"))
		}
	}
	for _, c := range []interface{ Close() error }{tw, gz, f} {
		if err := c.Close(); err != nil {
			t.Fatal(err)
		}
	}
	return p
}

func TestExtractStaysInside(t *testing.T) {
	// Each link points inside on its own; chained, they lead out.
	chain := []tar.Header{
		{Name: "l1", Typeflag: tar.TypeSymlink, Linkname: "."},
		{Name: "l1/l2", Typeflag: tar.TypeSymlink, Linkname: ".."},
		{Name: "l2/evil", Typeflag: tar.TypeReg, Mode: 0o644},
	}
	for name, entries := range map[string][]tar.Header{
		"chain":  chain,
		"dotdot": {{Name: "../evil", Typeflag: tar.TypeReg, Mode: 0o644}},
		"abs":    {{Name: "l", Typeflag: tar.TypeSymlink, Linkname: "/tmp"}},
	} {
		parent := t.TempDir()
		dst := filepath.Join(parent, "dst")
		_ = extract(writeTar(t, entries), dst)
		if _, err := os.Stat(filepath.Join(parent, "evil")); err == nil {
			t.Errorf("%s: wrote outside the destination", name)
		}
	}

	dst := filepath.Join(t.TempDir(), "dst")
	ok := []tar.Header{
		{Name: "a/", Typeflag: tar.TypeDir, Mode: 0o755},
		{Name: "a/b", Typeflag: tar.TypeReg, Mode: 0o644},
		{Name: "a/link", Typeflag: tar.TypeSymlink, Linkname: "b"},
		{Name: "c/d", Typeflag: tar.TypeReg, Mode: 0o600},
	}
	if err := extract(writeTar(t, ok), dst); err != nil {
		t.Fatal(err)
	}
	if b, err := os.ReadFile(filepath.Join(dst, "a", "link")); err != nil || string(b) != "x" {
		t.Errorf("a/link: %q %v", b, err)
	}
	if _, err := os.Stat(filepath.Join(dst, "c", "d")); err != nil {
		t.Error(err)
	}
}
