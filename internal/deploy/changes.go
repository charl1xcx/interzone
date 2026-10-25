package deploy

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"

	"interzone/internal/config"
)

// Changes is what an update would bring in: the playbook commits since
// the installed one, and the component versions they change.
type Changes struct {
	From, To string
	Total    int // commits; Commits holds the newest of them
	Commits  []Commit
	Versions []VersionChange
}

type Commit struct {
	Hash, Date, Subject string
}

type VersionChange struct {
	Name     string // variable or role, e.g. matrix_synapse_version
	Old, New string // "" when added or removed
}

const maxCommits = 200

// PreviewUpdate fetches the playbook without changing the checkout and
// compares it with what is installed.
func PreviewUpdate(ctx context.Context, c *config.Config) (*Changes, error) {
	dir := c.PlaybookDir
	if _, err := os.Stat(filepath.Join(dir, ".git")); err != nil {
		return nil, fmt.Errorf("the playbook isn't downloaded yet (or came from an offline bundle without git)")
	}
	git := func(args ...string) (string, error) {
		cmd := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...)
		cmd.Env = Env(c)
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		out, err := cmd.Output()
		if err != nil {
			return "", fmt.Errorf("git %s: %s", args[0], lastLine(stderr.String(), err))
		}
		return strings.TrimSpace(string(out)), nil
	}
	if _, err := git("fetch", "--quiet", c.Sources.PlaybookGit, defaultBranch(dir)); err != nil {
		return nil, err
	}
	ch := &Changes{}
	var err error
	if ch.From, err = git("rev-parse", "HEAD"); err != nil {
		return nil, err
	}
	if ch.To, err = git("rev-parse", "FETCH_HEAD"); err != nil {
		return nil, err
	}
	if ch.From == ch.To {
		return ch, nil
	}
	rng := ch.From + ".." + ch.To
	if n, err := git("rev-list", "--count", rng); err == nil {
		ch.Total, _ = strconv.Atoi(n)
	}
	log, err := git("log", "--max-count="+strconv.Itoa(maxCommits), "--date=short", "--format=%h%x09%ad%x09%s", rng)
	if err != nil {
		return nil, err
	}
	for _, l := range strings.Split(log, "\n") {
		if f := strings.SplitN(l, "\t", 3); len(f) == 3 {
			ch.Commits = append(ch.Commits, Commit{f[0], f[1], f[2]})
		}
	}

	files, _ := git("diff", "--name-only", ch.From, ch.To, "--", "requirements.yml", "roles/custom")
	show := func(rev, file string) string {
		out, _ := git("show", rev+":"+file)
		return out
	}
	for _, f := range strings.Split(files, "\n") {
		switch {
		case f == "requirements.yml":
			ch.Versions = append(ch.Versions, diffVersions(roleVersions(show(ch.From, f)), roleVersions(show(ch.To, f)))...)
		case strings.HasPrefix(f, "roles/custom/") && strings.HasSuffix(f, "/defaults/main.yml"):
			ch.Versions = append(ch.Versions, diffVersions(varVersions(show(ch.From, f)), varVersions(show(ch.To, f)))...)
		}
	}
	sort.Slice(ch.Versions, func(i, j int) bool { return ch.Versions[i].Name < ch.Versions[j].Name })
	return ch, nil
}

// roleVersions reads requirements.yml: role name → version.
func roleVersions(text string) map[string]string {
	var reqs []struct {
		Src, Name, Version string
	}
	_ = yaml.Unmarshal([]byte(text), &reqs)
	out := map[string]string{}
	for _, r := range reqs {
		name := r.Name
		if name == "" {
			name = strings.TrimSuffix(filepath.Base(r.Src), ".git")
		}
		out["role "+name] = r.Version
	}
	return out
}

// varVersions reads the *_version variables a defaults file pins.
func varVersions(text string) map[string]string {
	out := map[string]string{}
	for _, v := range config.ParseDefaults(text) {
		if strings.HasSuffix(v.Name, "_version") && !v.Derived && !strings.Contains(v.Default, "\n") {
			out[v.Name] = strings.Trim(v.Default, `"'`)
		}
	}
	return out
}

func diffVersions(old, new map[string]string) []VersionChange {
	var out []VersionChange
	for k, n := range new {
		if o := old[k]; o != n {
			out = append(out, VersionChange{k, o, n})
		}
	}
	for k, o := range old {
		if _, ok := new[k]; !ok {
			out = append(out, VersionChange{k, o, ""})
		}
	}
	return out
}

// FetchPlaybook clones the playbook if it isn't on this computer yet.
func FetchPlaybook(ctx context.Context, c *config.Config) error {
	if _, err := os.Stat(filepath.Join(c.PlaybookDir, "setup.yml")); err == nil {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(c.PlaybookDir), 0o755); err != nil {
		return err
	}
	cmd := exec.CommandContext(ctx, "git", "clone", "--depth", "1", c.Sources.PlaybookGit, c.PlaybookDir)
	cmd.Env = Env(c)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("git clone: %s", lastLine(string(out), err))
	}
	return nil
}
