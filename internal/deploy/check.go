package deploy

import (
	"context"
	"fmt"
	"net/http"
	"os/exec"
	"strings"
	"sync"
	"time"

	"interzone/internal/config"
	"interzone/internal/ipfs"
)

type Check struct {
	Name   string
	Target string
	OK     bool
	Detail string
}

// Preflight probes every configured source plus the local toolchain in parallel.
func Preflight(c *config.Config) []Check {
	type probe struct {
		name, target string
		fn           func(context.Context) (string, error)
	}
	var probes []probe
	add := func(name, target string, fn func(context.Context) (string, error)) {
		probes = append(probes, probe{name, target, fn})
	}

	for _, bin := range []string{"git", "ansible-playbook", "ansible-galaxy"} {
		bin := bin
		add("Local tool", bin, func(context.Context) (string, error) {
			p, err := lookPath(bin)
			if err != nil {
				if strings.HasPrefix(bin, "ansible") {
					return "", fmt.Errorf("not installed; will be installed from PyPI into a private venv")
				}
				return "", fmt.Errorf("not installed")
			}
			return p, nil
		})
	}
	s := c.Sources
	if s.IPFS {
		add("IPFS publish", ipfs.IndexURL, func(ctx context.Context) (string, error) {
			idx, err := indexFromWeb(ctx)
			if err != nil {
				return "", err
			}
			return "signed, from " + idx.Created, nil
		})
		add("IPFS entry node", "infamous.g0th.ir", func(ctx context.Context) (string, error) {
			return ipfsEntryReachable(ctx)
		})
	} else {
		add("Playbook git", s.PlaybookGit, func(ctx context.Context) (string, error) { return gitReachable(ctx, s.PlaybookGit) })
	}
	if s.RolesGitBase != "" && !s.IPFS {
		u := strings.TrimRight(s.RolesGitBase, "/") + "/mother-of-all-self-hosting/ansible-role-aux"
		add("Roles git mirror", s.RolesGitBase, func(ctx context.Context) (string, error) { return gitReachable(ctx, u) })
	}
	if s.UseRegistry && s.RegistryPrefix != "" && !s.IPFS {
		host := strings.SplitN(strings.TrimPrefix(strings.TrimPrefix(s.RegistryPrefix, "https://"), "http://"), "/", 2)[0]
		add("Container registry", s.RegistryPrefix, func(ctx context.Context) (string, error) {
			return httpReachable(ctx, "https://"+host+"/v2/")
		})
	}
	for _, m := range []struct {
		name, url string
		kind      config.MirrorKind
	}{
		{"Docker packages", s.DockerPackageURL, config.MirrorDocker},
		{"OS package mirror", s.APTMirror, config.MirrorOS},
		{"PyPI mirror", s.PyPIIndex, config.MirrorPyPI},
	} {
		if m.url != "" {
			add(m.name, m.url, func(ctx context.Context) (string, error) { return ProbeMirror(ctx, m.kind, m.url) })
		}
	}

	out := make([]Check, len(probes))
	var wg sync.WaitGroup
	for i, p := range probes {
		wg.Add(1)
		go func(i int, p probe) {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
			defer cancel()
			detail, err := p.fn(ctx)
			out[i] = Check{Name: p.name, Target: p.target, OK: err == nil, Detail: detail}
			if err != nil {
				out[i].Detail = err.Error()
			}
		}(i, p)
	}
	wg.Wait()
	return out
}

func gitReachable(ctx context.Context, url string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", "ls-remote", "--", url, "HEAD")
	cmd.Env = append(cmd.Environ(), "GIT_TERMINAL_PROMPT=0")
	b, err := cmd.Output()
	if err != nil {
		if ctx.Err() != nil {
			return "", fmt.Errorf("timed out")
		}
		return "", fmt.Errorf("unreachable")
	}
	if f := strings.Fields(string(b)); len(f) > 0 {
		return "HEAD " + f[0][:min(10, len(f[0]))], nil
	}
	return "reachable", nil
}

func httpReachable(ctx context.Context, url string) (string, error) {
	if !strings.Contains(url, "://") {
		url = "https://" + url
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return "", fmt.Errorf("timed out")
		}
		return "", fmt.Errorf("unreachable")
	}
	resp.Body.Close()
	if resp.StatusCode >= 500 {
		return "", fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return fmt.Sprintf("HTTP %d", resp.StatusCode), nil
}
