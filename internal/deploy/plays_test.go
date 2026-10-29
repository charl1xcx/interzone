package deploy

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// Every play interzone writes must at least parse: Ansible's argument
// splitter rejects some shell quoting that YAML accepts (the Dashboard's
// play did, from v0.5.26 to v0.5.28). Needs ansible-playbook; nothing is
// contacted.
func TestPlaysParse(t *testing.T) {
	bin, err := exec.LookPath("ansible-playbook")
	if err != nil {
		t.Skip("ansible-playbook isn't installed")
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "hosts"), []byte("[matrix_servers]\nmatrix.example.org ansible_host=192.0.2.1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for name, b := range map[string][]byte{"interzone-mirrors-rewrite.yml": mirrorsRewrite, "interzone-mirrors-metalinks.py": metalinksScript} {
		if err := os.WriteFile(filepath.Join(dir, name), b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	plays := map[string]string{
		"status": statusPlay, "alerts": alertsPlay, "backup": backupPlay, "restore-prep": restorePrepPlay,
		"restore-finish": restoreFinishPlay, "schedule": schedulePlay, "fetch-backups": fetchBackupsPlay,
		"uninstall": uninstallPlay, "disk": diskPlay, "tokens": tokensPlay, "mas": masPlay, "signup": signupPlay,
		"admin": adminPlay, "mirrors": string(mirrorsPlay),
	}
	for name, play := range plays {
		path := filepath.Join(dir, name+".yml")
		if err := os.WriteFile(path, []byte(play), 0o644); err != nil {
			t.Fatal(err)
		}
		cmd := exec.Command(bin, "--syntax-check", "-i", "hosts", path)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "ANSIBLE_LOCALHOST_WARNING=false", "ANSIBLE_HOME="+filepath.Join(dir, ".ansible"))
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Errorf("the %s play doesn't parse: %v\n%s", name, err, out)
		}
	}
}

func TestLastLine(t *testing.T) {
	for in, want := range map[string]string{
		"a\nb\n": "b",
		"[ERROR]: Error loading tasks: x\nOrigin: f.yml:6:7\n\n6 - name\n  ^ column 7\n": "Error loading tasks: x",
		"": "fallback",
	} {
		if got := lastLine(in, errors.New("fallback")); got != want {
			t.Errorf("lastLine(%q) = %q, want %q", in, got, want)
		}
	}
}
