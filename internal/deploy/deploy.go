// Package deploy drives git and Ansible to install and maintain the
// server, streaming progress back to the caller as events.
package deploy

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"interzone/internal/config"
)

// Version is the interzone version, recorded in offline bundles.
var Version = "dev"

type Step int

const (
	StepAnsible Step = iota
	StepPlaybook
	StepInventory
	StepRoles
	StepMirrors
	StepInstall
	StepSelfCheck
	StepRestart
	StepRegister
	StepBackup
	StepLogs
	StepWheels
	StepArchive
	StepImages
	StepSelfUpdate
	StepRestore
	StepSchedule
	StepFetchBackups
	StepStopOld
	StepCopyData
)

var stepNames = map[Step]string{
	StepAnsible:   "Prepare Ansible",
	StepPlaybook:  "Fetch playbook",
	StepInventory: "Write configuration",
	StepRoles:     "Install Ansible roles",
	StepMirrors:   "Point server at package mirrors",
	StepInstall:   "Run the playbook",
	StepSelfCheck: "Check that services work",
	StepRestart:   "Restart all services",
	StepRegister:  "Register the user",
	StepBackup:    "Back up the database",
	StepLogs:      "Fetch logs",
	StepWheels:    "Download Python packages",
	StepArchive:   "Write the bundle",

	StepImages:       "Check container images",
	StepSelfUpdate:   "Update interzone",
	StepRestore:      "Restore the database",
	StepSchedule:     "Set up scheduled backups",
	StepFetchBackups: "Download the server's backups",
	StepStopOld:      "Stop the old server",
	StepCopyData:     "Copy /matrix to the new server",
}

func (s Step) String() string { return stepNames[s] }

// Action is what a run does; each one has a fixed plan of steps.
type Action int

const (
	ActionInstall Action = iota
	ActionSaveOnly
	ActionSelfCheck
	ActionRestart
	ActionRegister
	ActionBackup
	ActionLogs
	ActionBundle
	ActionImages
	ActionRollback
	ActionSelfUpdate
	ActionRestore
	ActionSchedule
	ActionFetchBackups
	ActionMigrate
)

// Plan lists the steps an action runs, in order.
func Plan(a Action) []Step {
	switch a {
	case ActionSaveOnly:
		return []Step{StepPlaybook, StepInventory}
	case ActionSelfCheck:
		return []Step{StepAnsible, StepInventory, StepSelfCheck}
	case ActionRestart:
		return []Step{StepAnsible, StepInventory, StepRestart}
	case ActionRegister:
		return []Step{StepAnsible, StepInventory, StepRegister}
	case ActionBackup:
		return []Step{StepAnsible, StepInventory, StepBackup}
	case ActionLogs:
		return []Step{StepAnsible, StepInventory, StepLogs}
	case ActionBundle:
		return []Step{StepAnsible, StepPlaybook, StepRoles, StepWheels, StepArchive}
	case ActionImages:
		return []Step{StepAnsible, StepInventory, StepImages}
	case ActionRollback:
		return []Step{StepAnsible, StepPlaybook, StepInventory, StepRoles, StepBackup, StepInstall, StepSelfCheck}
	case ActionSelfUpdate:
		return []Step{StepSelfUpdate}
	case ActionRestore:
		return []Step{StepAnsible, StepInventory, StepBackup, StepRestore}
	case ActionSchedule:
		return []Step{StepAnsible, StepInventory, StepSchedule}
	case ActionFetchBackups:
		return []Step{StepAnsible, StepInventory, StepFetchBackups}
	case ActionMigrate:
		return []Step{StepAnsible, StepPlaybook, StepInventory, StepRoles, StepStopOld, StepCopyData, StepMirrors, StepInstall, StepSelfCheck}
	}
	return []Step{StepAnsible, StepPlaybook, StepInventory, StepRoles, StepImages, StepMirrors, StepInstall, StepSelfCheck}
}

type Event struct {
	Plan    []Step // the steps of this run; sent first
	Step    Step
	Started bool    // a step began
	Skipped bool    // a step was not needed
	Warn    bool    // a step finished with problems that don't stop the run
	Line    string  // a line of command output
	Note    bool    // Line is interzone's own message, not command output
	Task    string  // current Ansible task, when Line starts one
	Failed  bool    // Line reports a failed task
	Moved   bool    // migration: the data is on the new server, which is now the target
	Err     error   // the whole run failed
	Done    bool    // the whole run finished
	Result  *Result // set with Done
}

// Result is what a finished run produced besides log output.
type Result struct {
	Passed   []string // self-check: services that answered
	Problems []string // self-check: what failed
	File     string   // backup or bundle written

	Images        []string // image check: images the registries have
	MissingImages []string // image check: "ref: why"
	Version       string   // self-update: the version installed
}

type NewUser struct {
	Name     string
	Password string
	Admin    bool
}

type Options struct {
	Action  Action
	User    NewUser // ActionRegister
	Service string  // ActionLogs: systemd unit, e.g. matrix-synapse
	Output  string  // ActionBundle: archive path
	Backup  string  // ActionRestore: local dump file
	// ActionMigrate: the server to move to. The run works on a copy of the
	// config; the caller switches its own Target when the Moved event comes.
	NewTarget config.Target
	Force     bool // ActionSelfUpdate: reinstall even if up to date
}

func dataDir() string   { return config.DataDir() }
func venvDir() string   { return filepath.Join(dataDir(), "venv") }
func wheelsDir() string { return filepath.Join(dataDir(), "wheels") }

// BackupDir is where database backups are copied to.
func BackupDir() string { return filepath.Join(dataDir(), "backups") }

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

// Env is the environment for git and Ansible: no prompts, no colour, and
// github.com redirected to the roles mirror.
func Env(c *config.Config) []string {
	env := append(os.Environ(),
		"GIT_TERMINAL_PROMPT=0",
		"ANSIBLE_NOCOLOR=1",
		"ANSIBLE_FORCE_COLOR=0",
		"ANSIBLE_HOST_KEY_CHECKING=False",
		"ANSIBLE_STDOUT_CALLBACK=default",
		"PATH="+filepath.Join(venvDir(), "bin")+string(os.PathListSeparator)+os.Getenv("PATH"),
	)
	if base := c.Sources.RolesGitBase; base != "" && strings.TrimRight(base, "/") != strings.TrimRight(config.UpstreamGitHub, "/") {
		// Any git fetch of a github.com URL (requirements.yml) is redirected to the mirror.
		env = append(env,
			"GIT_CONFIG_COUNT=1",
			"GIT_CONFIG_KEY_0=url."+strings.TrimRight(base, "/")+"/.insteadOf",
			"GIT_CONFIG_VALUE_0="+config.UpstreamGitHub,
		)
	}
	if idx := c.Sources.PyPIIndex; idx != "" {
		env = append(env, "PIP_INDEX_URL="+idx)
	}
	if reuse := sshReuse(); reuse != nil {
		env = append(env, "ANSIBLE_SSH_ARGS=-C "+strings.Join(reuse, " "))
	}
	return env
}

type Runner struct {
	cfg    *config.Config
	opts   Options
	events chan Event
	env    []string
	result Result

	playbookUpdated bool
	onLine          func(Event) // extra parsing for the running command
}

// Start launches the run in the background. Events arrive on the returned
// channel, which is closed when the run ends.
func Start(ctx context.Context, cfg *config.Config, opts Options) <-chan Event {
	if opts.Action == ActionMigrate {
		cp := *cfg
		cfg = &cp
	}
	r := &Runner{cfg: cfg, opts: opts, events: make(chan Event, 256)}
	go func() {
		defer close(r.events)
		if err := r.run(ctx); err != nil {
			r.events <- Event{Err: err}
			return
		}
		res := r.result
		r.events <- Event{Done: true, Result: &res}
	}()
	return r.events
}

func (r *Runner) emit(e Event) { r.events <- e }

func (r *Runner) begin(s Step) { r.emit(Event{Step: s, Started: true}) }
func (r *Runner) skip(s Step, why string) {
	r.emit(Event{Step: s, Skipped: true, Line: why})
}
func (r *Runner) note(s Step, line string) { r.emit(Event{Step: s, Line: line, Note: true}) }

func (r *Runner) run(ctx context.Context) error {
	r.env = Env(r.cfg)
	fns := map[Step]func(context.Context) error{
		StepAnsible:   r.ansible,
		StepPlaybook:  r.playbook,
		StepInventory: r.inventory,
		StepRoles:     r.roles,
		StepMirrors:   r.mirrors,
		StepInstall:   r.install,
		StepSelfCheck: r.selfCheck,
		StepRestart:   r.restart,
		StepRegister:  r.register,
		StepBackup:    r.backup,
		StepLogs:      r.logs,
		StepWheels:    r.wheels,
		StepArchive:   r.archive,

		StepImages:       r.images,
		StepSelfUpdate:   r.selfUpdate,
		StepRestore:      r.restore,
		StepSchedule:     r.schedule,
		StepFetchBackups: r.fetchBackups,
		StepStopOld:      r.stopOld,
		StepCopyData:     r.copyData,
	}
	plan := Plan(r.opts.Action)
	r.emit(Event{Plan: plan})
	for _, st := range plan {
		if err := fns[st](ctx); err != nil {
			if ctx.Err() != nil {
				return fmt.Errorf("%s: aborted", st)
			}
			return fmt.Errorf("%s: %w", st, err)
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
	if hasWheels(wheelsDir()) {
		r.note(StepAnsible, "installing from the offline bundle's packages")
		args = []string{"install", "--no-index", "--find-links", wheelsDir(), "ansible", "passlib"}
	} else if idx := r.cfg.Sources.PyPIIndex; idx != "" {
		args = append(args, "--index-url", idx)
	}
	return r.exec(ctx, StepAnsible, "", filepath.Join(venvDir(), "bin", "pip"), args...)
}

func hasWheels(dir string) bool {
	m, _ := filepath.Glob(filepath.Join(dir, "*.whl"))
	return len(m) > 0
}

func (r *Runner) gitHead(dir string) string {
	cmd := exec.Command("git", "-C", dir, "rev-parse", "HEAD")
	b, _ := cmd.Output()
	return strings.TrimSpace(string(b))
}

func (r *Runner) playbook(ctx context.Context) error {
	r.begin(StepPlaybook)
	dir := r.cfg.PlaybookDir
	if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
		before := r.gitHead(dir)
		err := r.exec(ctx, StepPlaybook, dir, "git", "remote", "set-url", "origin", r.cfg.Sources.PlaybookGit)
		if pin := r.cfg.PinnedCommit; err == nil && pin != "" {
			err = r.checkout(ctx, dir, pin)
			r.playbookUpdated = before != r.gitHead(dir)
			return err
		}
		if err == nil && r.detached(dir) {
			// Back from a rollback: return to the branch.
			err = r.exec(ctx, StepPlaybook, dir, "git", "checkout", defaultBranch(dir))
		}
		if err == nil {
			err = r.exec(ctx, StepPlaybook, dir, "git", "pull", "--ff-only")
		}
		if err != nil && bundled(dir) && ctx.Err() == nil {
			r.note(StepPlaybook, "couldn't update the playbook; using the copy from the offline bundle")
			return nil
		}
		r.playbookUpdated = before != r.gitHead(dir)
		return err
	}
	if _, err := os.Stat(dir); err == nil && bundled(dir) {
		r.note(StepPlaybook, "using the playbook from the offline bundle")
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(dir), 0o755); err != nil {
		return err
	}
	r.playbookUpdated = true
	if err := r.exec(ctx, StepPlaybook, "", "git", "clone", "--depth", "1", r.cfg.Sources.PlaybookGit, dir); err != nil {
		return err
	}
	if pin := r.cfg.PinnedCommit; pin != "" {
		return r.checkout(ctx, dir, pin)
	}
	return nil
}

func (r *Runner) inventory(ctx context.Context) error {
	r.begin(StepInventory)
	c := r.cfg
	if _, err := os.Stat(filepath.Join(c.PlaybookDir, "setup.yml")); err != nil {
		return fmt.Errorf("no playbook in %s; run a full install first", c.PlaybookDir)
	}
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
		r.note(StepInventory, "self-signed certificate: "+cert)
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
		r.note(StepInventory, "wrote "+path)
	}
	return nil
}

func (r *Runner) roles(ctx context.Context) error {
	dir := r.cfg.PlaybookDir
	if bundled(dir) && !r.playbookUpdated && r.opts.Action != ActionBundle {
		if m, _ := filepath.Glob(filepath.Join(dir, "roles", "galaxy", "*")); len(m) > 0 {
			r.skip(StepRoles, "using the roles from the offline bundle")
			return nil
		}
	}
	r.begin(StepRoles)
	return r.exec(ctx, StepRoles, dir, "ansible-galaxy", "install", "-r", "requirements.yml", "-p", "roles/galaxy/", "--force")
}

func (r *Runner) mirrors(ctx context.Context) error {
	src := r.cfg.Sources
	if src.APTMirror == "" && src.DockerPackageURL == "" {
		r.skip(StepMirrors, "no OS or Docker package mirror configured")
		return nil
	}
	r.begin(StepMirrors)
	inv := filepath.Join(r.cfg.PlaybookDir, "inventory")
	_ = os.Remove(filepath.Join(inv, "interzone-apt-mirror.yml")) // the play before v0.3
	vars, err := json.MarshalIndent(mirrorVars(src), "", "  ")
	if err != nil {
		return err
	}
	for name, data := range map[string][]byte{
		"interzone-mirrors.yml":          mirrorsPlay,
		"interzone-mirrors-rewrite.yml":  mirrorsRewrite,
		"interzone-mirrors-metalinks.py": metalinksScript,
		"interzone-mirrors.json":         vars,
	} {
		if err := os.WriteFile(filepath.Join(inv, name), data, 0o644); err != nil {
			return err
		}
	}
	warned := false
	r.onLine = func(ev Event) {
		if m := playWarning.FindStringSubmatch(ev.Line); m != nil {
			warned = true
			r.note(StepMirrors, "! "+m[1])
		}
	}
	err = r.exec(ctx, StepMirrors, r.cfg.PlaybookDir, "ansible-playbook", "-i", "inventory/hosts",
		filepath.Join(inv, "interzone-mirrors.yml"), "-e", "@"+filepath.Join(inv, "interzone-mirrors.json"))
	r.onLine = nil
	if err == nil && warned {
		r.emit(Event{Step: StepMirrors, Warn: true})
	}
	return err
}

func (r *Runner) playbookRun(ctx context.Context, step Step, args ...string) error {
	r.begin(step)
	return r.exec(ctx, step, r.cfg.PlaybookDir, "ansible-playbook", append([]string{"-i", "inventory/hosts", "setup.yml"}, args...)...)
}

func (r *Runner) install(ctx context.Context) error {
	if err := r.playbookRun(ctx, StepInstall, "--tags=install-all,ensure-matrix-users-created,start"); err != nil {
		return err
	}
	r.signupToken(ctx)
	if err := RecordDeploy(r.cfg, r.gitHead(r.cfg.PlaybookDir)); err != nil {
		r.note(StepInstall, "! couldn't record this deploy for rollbacks: "+err.Error())
	}
	return nil
}

func (r *Runner) restart(ctx context.Context) error {
	return r.playbookRun(ctx, StepRestart, "--tags=restart")
}

func (r *Runner) register(ctx context.Context) error {
	u := r.opts.User
	admin := "no"
	if u.Admin {
		admin = "yes"
	}
	vars, _ := json.Marshal(map[string]string{"username": u.Name, "password": u.Password, "admin": admin})
	return r.playbookRun(ctx, StepRegister, "--tags=register-user", "--extra-vars", string(vars))
}

var validateVar = regexp.MustCompile(`(?m)^([a-z0-9_]+_self_check_validate_certificates):`)

// selfCheck runs MDAD's self-check. A failed check is reported in the
// result rather than failing the run: the install itself worked.
func (r *Runner) selfCheck(ctx context.Context) error {
	args := []string{"--tags=self-check"}
	if r.cfg.SSL.Mode == "selfsigned" {
		if f, err := r.noCertValidation(); err == nil {
			r.note(StepSelfCheck, "self-signed certificate: not validating certificates during the check")
			args = append(args, "--extra-vars", "@"+f)
		}
	}
	var task string
	r.onLine = func(ev Event) {
		if ev.Task != "" {
			task = ev.Task
			return
		}
		switch {
		case strings.HasPrefix(ev.Line, "ok:") && strings.HasPrefix(task, "Report working "):
			r.result.Passed = append(r.result.Passed, strings.TrimPrefix(task, "Report working "))
		case ev.Failed:
			r.result.Problems = append(r.result.Problems, FailureMessage(task, ev.Line))
		}
	}
	err := r.playbookRun(ctx, StepSelfCheck, args...)
	r.onLine = nil
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if err != nil && len(r.result.Problems) == 0 {
		r.result.Problems = append(r.result.Problems, "the self-check stopped early: "+err.Error())
	}
	if len(r.result.Problems) > 0 {
		r.emit(Event{Step: StepSelfCheck, Warn: true})
	}
	return nil
}

// FailureMessage pulls the "msg" out of an Ansible failure line, without
// the full error dump MDAD appends. Without one it's the task's name.
func FailureMessage(task, line string) string {
	msg := strings.TrimPrefix(task, "Fail if ")
	if _, js, ok := strings.Cut(line, "=> "); ok {
		var v struct {
			Msg string `json:"msg"`
		}
		if json.Unmarshal([]byte(js), &v) == nil && v.Msg != "" {
			msg = v.Msg
		}
	}
	msg, _, _ = strings.Cut(msg, " Full error:")
	return msg
}

// noCertValidation writes a vars file turning off certificate validation
// for every self-check the playbook defines.
func (r *Runner) noCertValidation() (string, error) {
	vars := map[string]bool{}
	dir := r.cfg.PlaybookDir
	files := []string{filepath.Join(dir, "group_vars", "matrix_servers")}
	for _, pat := range []string{"roles/*/*/defaults/main.yml", "roles/*/*/defaults/main/*.yml"} {
		m, _ := filepath.Glob(filepath.Join(dir, pat))
		files = append(files, m...)
	}
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		for _, m := range validateVar.FindAllSubmatch(b, -1) {
			vars[string(m[1])] = false
		}
	}
	if len(vars) == 0 {
		return "", fmt.Errorf("no self-check variables found")
	}
	b, _ := json.Marshal(vars)
	path := filepath.Join(dir, "inventory", "interzone-selfcheck-vars.json")
	return path, os.WriteFile(path, b, 0o600)
}

const backupPlay = `- name: Back up the Matrix Postgres database
  hosts: matrix_servers
  become: true
  gather_facts: false
  tasks:
    - name: Look for the bundled Postgres container
      ansible.builtin.command: docker ps -q --filter name=^matrix-postgres$
      register: interzone_pg
      changed_when: false

    - name: Stop if there is no bundled Postgres
      ansible.builtin.fail:
        msg: "No matrix-postgres container is running, so there is no database to dump. (External Postgres servers must be backed up separately.)"
      when: interzone_pg.stdout | trim == ''

    - name: Dump every database
      ansible.builtin.shell: >-
        set -o pipefail &&
        docker exec --env-file=/matrix/postgres/env-postgres-psql matrix-postgres
        /usr/local/bin/pg_dumpall -h matrix-postgres | gzip -c > /matrix/interzone-backup.sql.gz
      args:
        executable: /bin/bash

    - name: Copy the dump to this computer
      ansible.builtin.fetch:
        src: /matrix/interzone-backup.sql.gz
        dest: "{{ interzone_backup_dest }}"
        flat: true

    - name: Remove the dump from the server
      ansible.builtin.file:
        path: /matrix/interzone-backup.sql.gz
        state: absent
`

func (r *Runner) backup(ctx context.Context) error {
	if r.opts.Action != ActionBackup && r.cfg.Components["external-postgres"] {
		r.skip(StepBackup, "you use an external Postgres server; back it up where it runs")
		return nil
	}
	r.begin(StepBackup)
	if err := os.MkdirAll(BackupDir(), 0o700); err != nil {
		return err
	}
	dest := filepath.Join(BackupDir(), fmt.Sprintf("%s-%s.sql.gz", r.cfg.Domain, time.Now().Format("2006-01-02-1504")))
	play := filepath.Join(r.cfg.PlaybookDir, "inventory", "interzone-backup.yml")
	if err := os.WriteFile(play, []byte(backupPlay), 0o644); err != nil {
		return err
	}
	if err := r.exec(ctx, StepBackup, r.cfg.PlaybookDir, "ansible-playbook", "-i", "inventory/hosts", play,
		"-e", "interzone_backup_dest="+dest); err != nil {
		return err
	}
	_ = os.Chmod(dest, 0o600)
	r.result.File = dest
	return nil
}

var unitName = regexp.MustCompile(`^[A-Za-z0-9@._-]+$`)

func (r *Runner) logs(ctx context.Context) error {
	r.begin(StepLogs)
	if !unitName.MatchString(r.opts.Service) {
		return fmt.Errorf("invalid service name %q", r.opts.Service)
	}
	return r.exec(ctx, StepLogs, r.cfg.PlaybookDir, "ansible", "matrix_servers", "-i", "inventory/hosts", "--become",
		"-m", "ansible.builtin.command", "-a", "journalctl -u "+r.opts.Service+" -n 300 --no-pager -o short-iso")
}

// ListServices returns the matrix-* systemd services on the server. It
// asks over plain ssh (no Ansible start-up), which takes well under a
// second on the shared connection.
func ListServices(ctx context.Context, c *config.Config) ([]string, error) {
	cmd := targetCmd(ctx, c.Target, "systemctl list-units --type=service --all --no-legend --plain 'matrix-*'")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("couldn't reach the server (%s)", lastLine(stderr.String(), err))
	}
	var units []string
	for _, l := range strings.Split(string(out), "\n") {
		f := strings.Fields(l)
		if len(f) > 0 && strings.HasPrefix(f[0], "matrix-") && strings.HasSuffix(f[0], ".service") {
			units = append(units, strings.TrimSuffix(f[0], ".service"))
		}
	}
	if len(units) == 0 {
		return nil, fmt.Errorf("no matrix services found on the server")
	}
	return units, nil
}

// FollowLogs streams a service's journal, starting with its last 200
// lines, until ctx ends. The channel closes when the stream does; a last
// line starting with "✗ " says why, if it broke.
func FollowLogs(ctx context.Context, c *config.Config, unit string) (<-chan string, error) {
	if !unitName.MatchString(unit) {
		return nil, fmt.Errorf("invalid service name %q", unit)
	}
	cmd := targetCmd(ctx, c.Target, asRoot("journalctl -u "+unit+" -n 200 -f --no-pager -o short-iso"))
	cmd.Stdin = strings.NewReader(c.Target.BecomePass + "\n")
	out, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	cmd.Stderr = cmd.Stdout
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	ch := make(chan string, 256)
	go func() {
		defer close(ch)
		sc := bufio.NewScanner(out)
		sc.Buffer(make([]byte, 64*1024), 1024*1024)
		var last string
		for sc.Scan() {
			last = sc.Text()
			select {
			case ch <- last:
			case <-ctx.Done():
			}
		}
		if err := cmd.Wait(); err != nil && ctx.Err() == nil {
			ch <- "✗ the log stream ended (" + lastLine(last, err) + ")"
		}
	}()
	return ch, nil
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
			if r.onLine != nil {
				r.onLine(ev)
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
			if k, _, ok := strings.Cut(a, "="); ok && !strings.HasPrefix(a, "{") {
				a = k + "=••••"
			} else {
				a = "'<hidden>'"
			}
		}
		out[i] = a
	}
	return out
}
