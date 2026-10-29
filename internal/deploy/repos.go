package deploy

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"gopkg.in/yaml.v3"

	"interzone/internal/config"
)

// ── Repositories ───────────────────────────────────────────────────
//
// CheckRepos asks the playbook's git server, and the roles mirror for each
// role requirements.yml pins, whether the repository and its pinned
// version are there, the way a deploy would fetch them.

// Role is one entry of requirements.yml.
type Role struct {
	Name, Src, Version string
}

// Roles reads the playbook's requirements.yml, if it has been cloned.
func Roles(playbookDir string) ([]Role, error) {
	b, err := os.ReadFile(filepath.Join(playbookDir, "requirements.yml"))
	if err != nil {
		return nil, err
	}
	var reqs []Role
	if err := yaml.Unmarshal(b, &reqs); err != nil {
		return nil, err
	}
	for i, r := range reqs {
		reqs[i].Src = strings.TrimPrefix(r.Src, "git+")
		if r.Name == "" {
			reqs[i].Name = strings.TrimSuffix(filepath.Base(reqs[i].Src), ".git")
		}
	}
	return reqs, nil
}

// roleURL is where a deploy fetches src from: github.com rewritten to the
// roles mirror.
func roleURL(c *config.Config, src string) string {
	base := strings.TrimRight(c.Sources.RolesGitBase, "/")
	if base == "" {
		return src
	}
	if rest, ok := strings.CutPrefix(src, config.UpstreamGitHub); ok {
		return base + "/" + rest
	}
	return src
}

var commitish = regexp.MustCompile(`^[0-9a-f]{7,40}$`)

// CheckRepos checks the playbook and every role. Without a playbook clone
// only the playbook and the roles mirror itself are checked.
func CheckRepos(c *config.Config) []Check {
	type probe struct{ name, url, version string }
	probes := []probe{{"playbook", c.Sources.PlaybookGit, ""}}
	roles, err := Roles(c.PlaybookDir)
	if err != nil && c.Sources.RolesGitBase != "" {
		probes = append(probes, probe{"roles mirror", roleURL(c, config.UpstreamGitHub+"mother-of-all-self-hosting/ansible-role-aux"), ""})
	}
	for _, r := range roles {
		probes = append(probes, probe{r.Name, roleURL(c, r.Src), r.Version})
	}

	out := make([]Check, len(probes))
	sem := make(chan struct{}, 8)
	var wg sync.WaitGroup
	for i, p := range probes {
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			detail, err := gitHasVersion(ctx, p.url, p.version)
			out[i] = Check{Name: p.name, Target: p.url, OK: err == nil, Detail: detail}
			if err != nil {
				out[i].Detail = err.Error()
			}
		}()
	}
	wg.Wait()
	return out
}

// gitHasVersion checks that url answers and, for a tag or branch version,
// that it has it. A commit can't be asked for without fetching, so only
// the repository is checked then.
func gitHasVersion(ctx context.Context, url, version string) (string, error) {
	if version == "" || commitish.MatchString(version) {
		d, err := gitReachable(ctx, url)
		if err == nil && version != "" {
			d = "commit " + version[:min(10, len(version))]
		}
		return d, err
	}
	cmd := exec.CommandContext(ctx, "git", "ls-remote", "--", url, "refs/tags/"+version, "refs/heads/"+version)
	cmd.Env = append(cmd.Environ(), "GIT_TERMINAL_PROMPT=0")
	b, err := cmd.Output()
	switch {
	case ctx.Err() != nil:
		return "", fmt.Errorf("timed out")
	case err != nil:
		return "", fmt.Errorf("unreachable")
	case strings.TrimSpace(string(b)) == "":
		return "", fmt.Errorf("has no %s yet; the mirror may be behind", version)
	}
	return version, nil
}

// CheckSources probes the configured registry and package mirrors: what
// Preflight checks, without the local tools and the git repositories.
func CheckSources(c *config.Config) []Check {
	var out []Check
	for _, ch := range Preflight(c) {
		switch ch.Name {
		case "Local tool", "Playbook git", "Roles git mirror":
			continue
		}
		out = append(out, ch)
	}
	return out
}
