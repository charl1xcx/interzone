package deploy

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"interzone/internal/config"
)

func TestReadUsersCSV(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "people.csv")
	_ = os.WriteFile(path, []byte("username,password,admin\n@Alice:example.org, s3cret, admin\n# a comment\nbob\n\ncarol,,no\n"), 0o644)
	users, err := ReadUsersCSV(path)
	if err != nil || len(users) != 3 {
		t.Fatal(users, err)
	}
	if u := users[0]; u.Name != "alice" || u.Password != "s3cret" || !u.Admin {
		t.Errorf("%+v", u)
	}
	if u := users[2]; u.Name != "carol" || len(u.Password) != 16 || u.Admin {
		t.Errorf("%+v", u)
	}
	out, err := WritePasswords(path, "example.org", users)
	if err != nil || out != filepath.Join(dir, "people-passwords.csv") {
		t.Fatal(out, err)
	}
	b, _ := os.ReadFile(out)
	if st, _ := os.Stat(out); st.Mode().Perm() != 0o600 || !strings.Contains(string(b), "alice,@alice:example.org,s3cret,true") {
		t.Errorf("%v %q", st.Mode(), b)
	}

	for _, bad := range []string{"al ice\n", "bob\nbob\n", "username\n"} {
		_ = os.WriteFile(path, []byte(bad), 0o644)
		if _, err := ReadUsersCSV(path); err == nil {
			t.Errorf("took %q", bad)
		}
	}
}

// A config that changes what the server runs as never reaches vars.yml.
func TestInventoryRefusesBreaks(t *testing.T) {
	c := config.Fresh()
	c.Domain, c.Homeserver, c.PlaybookDir = "example.org", "synapse", t.TempDir()
	c.MarkDeployed()
	c.Domain = "other.org"
	r := &Runner{cfg: c}
	if err := r.writeInventory(StepInventory); err == nil || !strings.Contains(err.Error(), "can't be undone") {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(c.PlaybookDir, "inventory")); err == nil {
		t.Error("it wrote the inventory anyway")
	}
}
