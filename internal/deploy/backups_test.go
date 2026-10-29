package deploy

import (
	"os"
	"path/filepath"
	"testing"
)

func TestListBackupsKeepsDomainsApart(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	if err := os.MkdirAll(BackupDir(), 0o700); err != nil {
		t.Fatal(err)
	}
	for _, n := range []string{"a.org-2026-01-02-0304.sql.gz", "a.org-b.net-2026-01-02-0304.sql.gz"} {
		if err := os.WriteFile(filepath.Join(BackupDir(), n), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if l := ListBackups("a.org"); len(l) != 1 || filepath.Base(l[0].Path) != "a.org-2026-01-02-0304.sql.gz" {
		t.Errorf("a.org: %+v", l)
	}
	if l := ListBackups("a.org-b.net"); len(l) != 1 {
		t.Errorf("a.org-b.net: %+v", l)
	}
}
