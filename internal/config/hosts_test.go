package config

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestDNSRecords(t *testing.T) {
	dir := t.TempDir()
	for path, body := range map[string]string{
		"group_vars/matrix_servers": "matrix_server_fqn_matrix: \"matrix.{{ matrix_domain }}\"\n" +
			"matrix_server_fqn_element: \"element.{{ matrix_domain }}\"\n" +
			"matrix_client_element_hostname: \"{{ matrix_server_fqn_element }}\"\n" +
			"matrix_ketesa_hostname: \"{{ matrix_server_fqn_matrix }}\"\n" +
			"jitsi_hostname: \"{{ 'jitsi.' + matrix_domain }}\"\n",
		"roles/custom/matrix-client-cinny/defaults/main.yml": "cinny_hostname: \"cinny.{{ matrix_domain }}\"\n",
	} {
		_ = os.MkdirAll(filepath.Dir(filepath.Join(dir, path)), 0o755)
		if err := os.WriteFile(filepath.Join(dir, path), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	c := Default()
	c.Domain, c.PlaybookDir = "example.org", dir
	c.Components = map[string]bool{"element": true, "cinny": true, "ketesa": true, "jitsi": true, "etherpad": true}
	c.ExtraYAML = "cinny_hostname: chat.other.net"
	got := c.DNSRecords()
	// cinny moved; Ketesa on matrix.; Jitsi's expression and Etherpad's
	// missing variable fall back to the usual subdomain.
	want := []string{"matrix.example.org", "element.example.org", "chat.other.net", "etherpad.example.org", "jitsi.example.org"}
	slices.Sort(got[1:])
	slices.Sort(want[1:])
	if !slices.Equal(got, want) {
		t.Errorf("got %v", got)
	}

	if got := c.ServicesOn("matrix.example.org"); got != "Synapse, Ketesa admin panel" {
		t.Errorf("on matrix.: %q", got)
	}
	if got := c.ServicesOn("chat.other.net"); got != "Cinny" {
		t.Errorf("on chat.: %q", got)
	}

	// Without a playbook, the usual subdomains.
	c.PlaybookDir = ""
	if got := c.DNSRecords(); !slices.Contains(got, "chat.other.net") || !slices.Contains(got, "element.example.org") {
		t.Errorf("no playbook: %v", got)
	}
}

func TestLocalHostsUseSystemPython(t *testing.T) {
	py := filepath.Join(t.TempDir(), "python3")
	old := systemPython
	t.Cleanup(func() { systemPython = old })
	systemPython = py
	c := Default()
	c.Domain = "example.org"
	if got := c.HostsINI(); strings.Contains(got, "ansible_python_interpreter") {
		t.Errorf("no python there, but got %q", got)
	}
	_ = os.WriteFile(py, nil, 0o755)
	if got := c.HostsINI(); !strings.Contains(got, "ansible_python_interpreter="+py) {
		t.Errorf("got %q", got)
	}
	c.Target = Target{Mode: "ssh", Host: "192.0.2.1", User: "root"}
	if got := c.HostsINI(); strings.Contains(got, "ansible_python_interpreter") {
		t.Errorf("ssh got %q", got)
	}
}

// The inventory line is read back the way Ansible reads it, shell-style:
// a quote in the sudo password used to come out without it.
func TestHostsINIQuoting(t *testing.T) {
	c := Default()
	c.Domain = "example.org"
	c.Target = Target{Mode: "ssh", Host: "192.0.2.1", User: "deploy", Port: "2222", SSHKey: "~/my keys/id", BecomePass: `it's "a" \pass #1`}
	line := strings.Split(strings.TrimSpace(c.HostsINI()), "\n")
	fields := splitHostLine(line[len(line)-1])
	got := map[string]string{}
	for _, f := range fields[1:] {
		k, v, _ := strings.Cut(f, "=")
		got[k] = v
	}
	for k, want := range map[string]string{"ansible_host": "192.0.2.1", "ansible_ssh_user": "deploy", "ansible_port": "2222",
		"ansible_ssh_private_key_file": "~/my keys/id", "ansible_become_password": c.Target.BecomePass} {
		if got[k] != want {
			t.Errorf("%s = %q, want %q", k, got[k], want)
		}
	}
	bin, err := exec.LookPath("ansible-inventory")
	if err != nil {
		return
	}
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "hosts"), []byte(c.HostsINI()), 0o644)
	cmd := exec.Command(bin, "-i", "hosts", "--host", c.HostName())
	cmd.Dir, cmd.Env = dir, append(os.Environ(), "ANSIBLE_HOME="+filepath.Join(dir, ".ansible"))
	out, err := cmd.Output()
	var vars map[string]any
	if err != nil || json.Unmarshal(out, &vars) != nil {
		t.Fatalf("ansible-inventory: %v\n%s", err, out)
	}
	if vars["ansible_become_password"] != c.Target.BecomePass || vars["ansible_ssh_private_key_file"] != "~/my keys/id" {
		t.Errorf("Ansible reads %v", vars)
	}
}
