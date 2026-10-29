package deploy

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"interzone/internal/config"
)

// tagsSafe are the characters playbook tags are made of.
var tagsSafe = regexp.MustCompile(`^[a-z0-9_,-]+$`)

// tags runs playbook tags, as `just run-tags` does: maintenance from the
// playbook's docs (vacuum, Postgres upgrade, state compression), starting,
// stopping or reinstalling services, or any tags typed in.
func (r *Runner) tags(ctx context.Context) error {
	t := strings.ReplaceAll(r.opts.Tags, " ", "")
	if !tagsSafe.MatchString(t) {
		return fmt.Errorf("%q aren't playbook tags (letters, digits, - and _, separated by commas)", r.opts.Tags)
	}
	args := []string{"--tags=" + t}
	if len(r.opts.Vars) > 0 {
		b, _ := json.Marshal(r.opts.Vars)
		args = append(args, "--extra-vars", string(b))
	}
	return r.playbookRun(ctx, StepTags, args...)
}

const uninstallPlay = `- name: Remove everything the playbook installed
  hosts: matrix_servers
  become: true
  gather_facts: false
  tasks:
    - name: Run remove-all
      ansible.builtin.shell: printf '%s\n' 'Yes, I really want to remove everything!' | {{ matrix_base_data_path | default('/matrix') }}/bin/remove-all
      args:
        removes: "{{ matrix_base_data_path | default('/matrix') }}/bin/remove-all"
`

// uninstall runs the remove-all script the playbook puts on the server
// (docs/uninstalling.md): every matrix-* service, the Docker images and
// /matrix go. Docker itself stays.
func (r *Runner) uninstall(ctx context.Context) error {
	return r.play(ctx, StepUninstall, "uninstall", uninstallPlay)
}

const diskPlay = `- name: Measure the disk
  hosts: matrix_servers
  become: true
  gather_facts: false
  tasks:
    - name: Measure
      ansible.builtin.shell: |
        df -h / /matrix 2>/dev/null | awk '!seen[$0]++'
        echo
        echo "Largest under /matrix:"
        du -xsh /matrix/* 2>/dev/null | sort -rh | head -12
        if [ -d /matrix/synapse/storage/media-store ]; then
          echo
          echo "Synapse media (remote_* is media cached from other servers):"
          du -xsh /matrix/synapse/storage/media-store/* 2>/dev/null | sort -rh
        fi
        echo
        docker system df 2>/dev/null
      register: interzone_disk
      changed_when: false
    - name: Disk usage
      ansible.builtin.debug:
        var: interzone_disk.stdout_lines
`

// disk shows what takes the server's disk space: free space, the largest
// directories under /matrix, Synapse's media and Docker's images.
func (r *Runner) disk(ctx context.Context) error {
	return r.play(ctx, StepDisk, "disk", diskPlay)
}

// play runs one of interzone's own plays against the server.
func (r *Runner) play(ctx context.Context, step Step, name, play string) error {
	r.begin(step)
	path := filepath.Join(r.cfg.PlaybookDir, "inventory", "interzone-"+name+".yml")
	if err := os.WriteFile(path, []byte(play), 0o644); err != nil {
		return err
	}
	return r.exec(ctx, step, r.cfg.PlaybookDir, "ansible-playbook", "-i", "inventory/hosts", path)
}

// ServiceGroups are the service groups the playbook can start, stop or
// install on their own (`just start-group GROUP`), read from its
// group_vars, sorted, without the "matrix" group every service is in.
func ServiceGroups(playbookDir string) []string {
	b, _ := os.ReadFile(filepath.Join(playbookDir, "group_vars", "matrix_servers"))
	var out []string
	for _, m := range regexp.MustCompile(`'groups':\s*\[([^\]]*)\]`).FindAllStringSubmatch(string(b), -1) {
		for _, g := range strings.Split(m[1], ",") {
			g = strings.TrimSpace(g)
			if !strings.HasPrefix(g, "'") { // an expression, not a name
				continue
			}
			g = strings.Trim(g, "'")
			if g != "" && g != "matrix" && tagsSafe.MatchString(g) && !slices.Contains(out, g) {
				out = append(out, g)
			}
		}
	}
	slices.Sort(out)
	return out
}

// DatabaseShell is psql on the bundled Postgres, through the playbook's
// /matrix/postgres/bin/cli (docs/maintenance-postgres.md), for running in
// this terminal.
func DatabaseShell(c *config.Config) *exec.Cmd {
	const cli = "/matrix/postgres/bin/cli"
	if c.Target.Mode != "ssh" {
		if os.Geteuid() == 0 {
			return exec.Command(cli)
		}
		return exec.Command("sudo", cli)
	}
	run := "sudo " + cli
	if c.Target.User == "root" {
		run = cli
	}
	return exec.Command("ssh", append(append([]string{"-t"}, sshArgs(c.Target)...), run)...)
}
