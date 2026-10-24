// Package deploy drives git and Ansible to install the playbook, streaming
// progress back to the UI as events.
package deploy

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"interzone/internal/config"
)

type Step int

const (
	StepAnsible Step = iota
	StepPlaybook
	StepInventory
	StepRoles
	StepAPT
	StepInstall
	StepDone
)

var StepNames = []string{
	"Prepare Ansible",
	"Fetch playbook",
	"Write configuration",
	"Install Ansible roles",
	"Point server at APT mirror",
	"Run the playbook",
}

type Event struct {
	Step    Step
	Started bool   // a step began
	Skipped bool   // a step was not needed
	Line    string // a line of command output
	Task    string // current Ansible task, when Line starts one
	Failed  bool   // Line reports a failed task
	Err     error  // the whole run failed
	Done    bool   // the whole run finished successfully
}

type Options struct {
	SaveOnly bool // write configuration but don't run Ansible
}

func venvDir() string { return filepath.Join(filepath.Dir(config.Default().PlaybookDir), "venv") }

// lookPath also finds tools in the venv interzone creates when Ansible is missing.
func lookPath(bin string) (string, error) {
	if p, err := exec.LookPath(bin); err == nil {
		return p, nil
	}
	p := filepath.Join(venvDir(), "bin", bin)
	if _, err := os.Stat(p); err == nil {
		return p, nil
	}
	return "", exec.ErrNotFound
}

type Runner struct {
	cfg    *config.Config
	opts   Options
	events chan Event
	env    []string
}

// Start launches the deployment in the background. Events arrive on the
// returned channel, which is closed when the run ends.
func Start(ctx context.Context, cfg *config.Config, opts Options) <-chan Event {
	r := &Runner{cfg: cfg, opts: opts, events: make(chan Event, 256)}
	go func() {
		defer close(r.events)
		if err := r.run(ctx); err != nil {
			r.events <- Event{Err: err}
			return
		}
		r.events <- Event{Done: true, Step: StepDone}
	}()
	return r.events
}

func (r *Runner) emit(e Event) { r.events <- e }

func (r *Runner) begin(s Step) { r.emit(Event{Step: s, Started: true}) }
func (r *Runner) skip(s Step, why string) {
	r.emit(Event{Step: s, Skipped: true, Line: why})
}

func (r *Runner) run(ctx context.Context) error {
	r.env = append(os.Environ(),
		"GIT_TERMINAL_PROMPT=0",
		"ANSIBLE_NOCOLOR=1",
		"ANSIBLE_FORCE_COLOR=0",
		"ANSIBLE_HOST_KEY_CHECKING=False",
		"ANSIBLE_STDOUT_CALLBACK=default",
		"PATH="+filepath.Join(venvDir(), "bin")+string(os.PathListSeparator)+os.Getenv("PATH"),
	)
	if base := r.cfg.Sources.RolesGitBase; base != "" && strings.TrimRight(base, "/") != strings.TrimRight(config.UpstreamGitHub, "/") {
		// Any git fetch of a github.com URL (requirements.yml) is redirected to the mirror.
		r.env = append(r.env,
			"GIT_CONFIG_COUNT=1",
			"GIT_CONFIG_KEY_0=url."+strings.TrimRight(base, "/")+"/.insteadOf",
			"GIT_CONFIG_VALUE_0="+config.UpstreamGitHub,
		)
	}
	if idx := r.cfg.Sources.PyPIIndex; idx != "" {
		r.env = append(r.env, "PIP_INDEX_URL="+idx)
	}

	steps := []func(context.Context) error{r.ansible, r.playbook, r.inventory, r.roles, r.apt, r.install}
	for i, fn := range steps {
		if r.opts.SaveOnly && (Step(i) == StepAnsible || Step(i) >= StepRoles) {
			r.skip(Step(i), "save-only mode")
			continue
		}
		if err := fn(ctx); err != nil {
			return fmt.Errorf("%s: %w", StepNames[i], err)
		}
	}
	return nil
}

func (r *Runner) ansible(ctx context.Context) error {
	if _, err := lookPath("ansible-playbook"); err == nil {
		r.skip(StepAnsible, "ansible is already installed")
		return nil
	}
	r.begin(StepAnsible)
	if err := r.exec(ctx, StepAnsible, "", "python3", "-m", "venv", venvDir()); err != nil {
		return err
	}
	args := []string{"install", "--upgrade", "ansible", "passlib"}
	if idx := r.cfg.Sources.PyPIIndex; idx != "" {
		args = append(args, "--index-url", idx)
	}
	return r.exec(ctx, StepAnsible, "", filepath.Join(venvDir(), "bin", "pip"), args...)
}

func (r *Runner) playbook(ctx context.Context) error {
	r.begin(StepPlaybook)
	dir := r.cfg.PlaybookDir
	if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
		if err := r.exec(ctx, StepPlaybook, dir, "git", "remote", "set-url", "origin", r.cfg.Sources.PlaybookGit); err != nil {
			return err
		}
		return r.exec(ctx, StepPlaybook, dir, "git", "pull", "--ff-only")
	}
	if err := os.MkdirAll(filepath.Dir(dir), 0o755); err != nil {
		return err
	}
	return r.exec(ctx, StepPlaybook, "", "git", "clone", "--depth", "1", r.cfg.Sources.PlaybookGit, dir)
}

func (r *Runner) inventory(ctx context.Context) error {
	r.begin(StepInventory)
	c := r.cfg
	inv := filepath.Join(c.PlaybookDir, "inventory")
	hostDir := filepath.Join(inv, "host_vars", c.HostName())
	if err := os.MkdirAll(hostDir, 0o700); err != nil {
		return err
	}

	var cert, key string
	switch c.SSL.Mode {
	case "selfsigned":
		var err error
		cert, key, err = config.SelfSignedCert(filepath.Join(config.Dir(), "ssl", c.Domain), c.Domain)
		if err != nil {
			return fmt.Errorf("self-signed certificate: %w", err)
		}
		r.emit(Event{Step: StepInventory, Line: "self-signed certificate: " + cert})
	case "custom":
		cert, key = c.SSL.CertPath, c.SSL.KeyPath
	}

	vars := c.VarsYAML(config.MigrationVersion(c.PlaybookDir), cert, key)
	files := map[string]string{
		filepath.Join(hostDir, "vars.yml"): vars,
		filepath.Join(inv, "hosts"):        c.HostsINI(),
	}
	for path, body := range files {
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			return err
		}
		r.emit(Event{Step: StepInventory, Line: "wrote " + path})
	}
	return nil
}

func (r *Runner) roles(ctx context.Context) error {
	r.begin(StepRoles)
	return r.exec(ctx, StepRoles, r.cfg.PlaybookDir, "ansible-galaxy", "install", "-r", "requirements.yml", "-p", "roles/galaxy/", "--force")
}

const aptPlay = `- name: Point APT at the in-country mirror
  hosts: matrix_servers
  become: true
  gather_facts: true
  tasks:
    - name: Find APT source files
      ansible.builtin.find:
        paths: [/etc/apt, /etc/apt/sources.list.d]
        patterns: ["sources.list", "*.list", "*.sources"]
      register: interzone_apt_sources
      when: ansible_facts.os_family == 'Debian'

    - name: Rewrite Debian/Ubuntu archive hosts to the mirror
      ansible.builtin.replace:
        path: "{{ item.path }}"
        regexp: 'https?://(?:(?:[a-z0-9-]+\.)*(?:archive|security|ports)\.ubuntu\.com|(?:[a-z0-9-]+\.)*debian\.org)'
        replace: "{{ interzone_apt_mirror }}"
        backup: true
      loop: "{{ interzone_apt_sources.files | default([]) }}"
      when: ansible_facts.os_family == 'Debian'

    - name: Warn about unsupported distributions
      ansible.builtin.debug:
        msg: "APT mirror skipped: {{ ansible_facts.distribution }} is not Debian-based."
      when: ansible_facts.os_family != 'Debian'
`

func (r *Runner) apt(ctx context.Context) error {
	mirror := strings.TrimRight(r.cfg.Sources.APTMirror, "/")
	if mirror == "" {
		r.skip(StepAPT, "no APT mirror configured")
		return nil
	}
	r.begin(StepAPT)
	play := filepath.Join(r.cfg.PlaybookDir, "inventory", "interzone-apt-mirror.yml")
	if err := os.WriteFile(play, []byte(aptPlay), 0o644); err != nil {
		return err
	}
	return r.exec(ctx, StepAPT, r.cfg.PlaybookDir, "ansible-playbook", "-i", "inventory/hosts", play,
		"-e", "interzone_apt_mirror="+mirror)
}

func (r *Runner) install(ctx context.Context) error {
	r.begin(StepInstall)
	return r.exec(ctx, StepInstall, r.cfg.PlaybookDir, "ansible-playbook", "-i", "inventory/hosts", "setup.yml",
		"--tags=install-all,ensure-matrix-users-created,start")
}

var taskLine = regexp.MustCompile(`^(?:TASK|PLAY|RUNNING HANDLER) \[(.*)\]`)

// exec runs a command and streams every output line as an event.
func (r *Runner) exec(ctx context.Context, step Step, dir, name string, args ...string) error {
	if p, err := lookPath(name); err == nil {
		name = p
	}
	r.emit(Event{Step: step, Line: "$ " + filepath.Base(name) + " " + strings.Join(redact(args), " ")})
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	cmd.Env = r.env
	pr, pw := io.Pipe()
	cmd.Stdout = pw
	cmd.Stderr = pw
	if err := cmd.Start(); err != nil {
		return err
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		sc := bufio.NewScanner(pr)
		sc.Buffer(make([]byte, 64*1024), 1024*1024)
		for sc.Scan() {
			line := strings.TrimRight(sc.Text(), " *")
			if strings.TrimSpace(line) == "" {
				continue
			}
			ev := Event{Step: step, Line: line}
			if m := taskLine.FindStringSubmatch(line); m != nil {
				ev.Task = m[1]
			}
			if strings.HasPrefix(line, "fatal:") || strings.HasPrefix(line, "failed:") {
				ev.Failed = true
			}
			r.emit(ev)
		}
	}()
	err := cmd.Wait()
	pw.Close()
	<-done
	return err
}

func redact(args []string) []string {
	out := make([]string, len(args))
	for i, a := range args {
		if strings.Contains(a, "password") {
			a = strings.SplitN(a, "=", 2)[0] + "=••••"
		}
		out[i] = a
	}
	return out
}
