package deploy

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"interzone/internal/config"
)

// A dry run is an update in Ansible's check mode: the newest playbook is
// checked out beside yours (so yours stays as it is), its roles fetched,
// and the playbook run with --check, which reports what it would change
// on the server without changing it.

// DryRunDir is the playbook copy dry runs use.
func DryRunDir(c *config.Config) string { return strings.TrimRight(c.PlaybookDir, "/") + "-dry-run" }

func (r *Runner) dryPlaybook(ctx context.Context) error {
	r.begin(StepDryPlaybook)
	dir := r.cfg.PlaybookDir
	if _, err := os.Stat(filepath.Join(dir, ".git")); err != nil {
		_ = os.RemoveAll(dir)
		// --shared borrows the objects of your copy, so this takes no time or space.
		src := strings.TrimSuffix(dir, "-dry-run")
		if err := r.exec(ctx, StepDryPlaybook, "", "git", "clone", "--quiet", "--shared", src, dir); err != nil {
			return err
		}
	}
	if err := r.exec(ctx, StepDryPlaybook, dir, "git", "fetch", "--quiet", r.cfg.Sources.PlaybookGit, defaultBranch(dir)); err != nil {
		return err
	}
	return r.exec(ctx, StepDryPlaybook, dir, "git", "checkout", "--quiet", "--force", "--detach", "FETCH_HEAD")
}

func (r *Runner) dryRun(ctx context.Context) error {
	task := ""
	r.onLine = func(ev Event) {
		switch {
		case taskLine.MatchString(ev.Line):
			task = taskLine.FindStringSubmatch(ev.Line)[1]
		case strings.HasPrefix(ev.Line, "changed:") && task != "" && !slices.Contains(r.result.WouldChange, task):
			r.result.WouldChange = append(r.result.WouldChange, task)
		}
	}
	err := r.playbookRun(ctx, StepDryRun, "--tags=install-all,ensure-matrix-users-created,start", "--check")
	r.onLine = nil
	if err != nil && ctx.Err() == nil && task != "" {
		// Check mode can't get past tasks that need an earlier one's real
		// result; what was checked up to there still stands.
		r.result.StoppedAt = task
		r.note(StepDryRun, "! check mode stopped at "+task+"; the tasks after it weren't checked")
		return nil
	}
	return err
}
