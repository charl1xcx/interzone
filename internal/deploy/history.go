package deploy

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"interzone/internal/config"
)

// Every successful install records the playbook commit and the answers it
// ran with, so a later one can be rolled back. Records live in
// <config dir>/history/<server>/<time>/ and the newest keepHistory are kept.

const keepHistory = 10

// Deploy is one recorded install.
type Deploy struct {
	Dir     string    `json:"-"`
	Time    time.Time `json:"time"`
	Commit  string    `json:"commit"`
	Subject string    `json:"subject"` // the commit's first line
	Version string    `json:"interzone"`
	Domain  string    `json:"domain"`
	// Homeserver and Components summarize the answers for the history list.
	Homeserver string `json:"homeserver"`
	Components int    `json:"components"`
}

func historyDir() string { return filepath.Join(config.Dir(), "history", config.Active()) }

// RecordDeploy saves the answers and playbook commit of a finished install.
func RecordDeploy(c *config.Config, commit string) error {
	if commit == "" {
		return fmt.Errorf("the playbook has no git commit")
	}
	now := time.Now()
	dir := filepath.Join(historyDir(), now.Format("2006-01-02-150405"))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	snap := *c
	snap.PinnedCommit = ""
	if err := snap.SaveFile(filepath.Join(dir, "config.json")); err != nil {
		return err
	}
	n := 0
	for _, on := range c.Components {
		if on {
			n++
		}
	}
	subject, _ := exec.Command("git", "-C", c.PlaybookDir, "log", "-1", "--format=%s", commit).Output()
	d := Deploy{Time: now, Commit: commit, Subject: strings.TrimSpace(string(subject)), Version: Version,
		Domain: c.Domain, Homeserver: c.Homeserver, Components: n}
	b, _ := json.MarshalIndent(d, "", "  ")
	if err := os.WriteFile(filepath.Join(dir, "deploy.json"), b, 0o600); err != nil {
		return err
	}
	all := History()
	for _, old := range all[min(len(all), keepHistory):] {
		_ = os.RemoveAll(old.Dir)
	}
	return nil
}

// History lists the recorded installs of the active server, newest first.
func History() []Deploy {
	m, _ := filepath.Glob(filepath.Join(historyDir(), "*", "deploy.json"))
	var out []Deploy
	for _, f := range m {
		b, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		var d Deploy
		if json.Unmarshal(b, &d) != nil {
			continue
		}
		d.Dir = filepath.Dir(f)
		out = append(out, d)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Time.After(out[j].Time) })
	return out
}

// RollbackConfig returns the answers of a recorded install, pinned to its
// playbook commit, keeping the current machine-specific paths.
func RollbackConfig(cur *config.Config, d Deploy) (*config.Config, error) {
	c, err := config.LoadFile(filepath.Join(d.Dir, "config.json"))
	if err != nil {
		return nil, err
	}
	c.PlaybookDir = cur.PlaybookDir
	c.Target = cur.Target // the server may have moved since
	// Not part of what was deployed: keep today's.
	c.Backups, c.AdminToken = cur.Backups, cur.AdminToken
	// What the server runs as stays: a rollback past it would break it.
	c.Deployed = cur.Deployed
	c.PinnedCommit = d.Commit
	return c, nil
}

func (r *Runner) detached(dir string) bool {
	return exec.Command("git", "-C", dir, "symbolic-ref", "-q", "HEAD").Run() != nil
}

// defaultBranch is the branch the clone started on.
func defaultBranch(dir string) string {
	out, err := exec.Command("git", "-C", dir, "rev-parse", "--abbrev-ref", "origin/HEAD").Output()
	if b := strings.TrimPrefix(strings.TrimSpace(string(out)), "origin/"); err == nil && b != "" && b != "HEAD" {
		return b
	}
	for _, b := range []string{"master", "main"} {
		if exec.Command("git", "-C", dir, "rev-parse", "--verify", "-q", "refs/heads/"+b).Run() == nil {
			return b
		}
	}
	return "master"
}

// checkout puts the playbook at commit, fetching it first if the shallow
// clone doesn't have it.
func (r *Runner) checkout(ctx context.Context, dir, commit string) error {
	r.note(StepPlaybook, "holding the playbook at "+short(commit)+" (rollback)")
	if exec.Command("git", "-C", dir, "cat-file", "-e", commit+"^{commit}").Run() != nil {
		if err := r.exec(ctx, StepPlaybook, dir, "git", "fetch", "origin", commit); err != nil {
			if err := r.exec(ctx, StepPlaybook, dir, "git", "fetch", "--unshallow", "origin"); err != nil {
				return fmt.Errorf("couldn't fetch commit %s: %w", short(commit), err)
			}
		}
	}
	return r.exec(ctx, StepPlaybook, dir, "git", "checkout", "--detach", commit)
}

func short(commit string) string { return commit[:min(len(commit), 10)] }

// Backfill records what an install from before Config.Deployed existed
// runs as, from the answers of its last successful deploy.
func Backfill(c *config.Config) {
	if c.Deployed != nil {
		return
	}
	h := History()
	if len(h) == 0 {
		return
	}
	if snap, err := config.LoadFile(filepath.Join(h[0].Dir, "config.json")); err == nil {
		snap.MarkDeployed()
		c.Deployed = snap.Deployed
	}
}
