package deploy

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"interzone/internal/config"
)

// TaskResult is one host's result for one task, as Ansible's json callback
// reports it.
type TaskResult map[string]json.RawMessage

func (t TaskResult) String(key string) string {
	var s string
	_ = json.Unmarshal(t[key], &s)
	return s
}

// runPlayJSON runs a play against the server with Ansible's json output
// and returns each named task's result. vars are passed through a
// temporary owner-only file so secrets never show up in ps.
func runPlayJSON(ctx context.Context, c *config.Config, name, play string, vars map[string]any, args ...string) (map[string]TaskResult, error) {
	bin, err := lookPath("ansible-playbook")
	if err != nil {
		return nil, fmt.Errorf("ansible isn't installed yet; run a full install first")
	}
	if _, err := os.Stat(filepath.Join(c.PlaybookDir, "inventory", "hosts")); err != nil {
		return nil, fmt.Errorf("no inventory in %s; run a full install first", c.PlaybookDir)
	}
	dir := c.PlaybookDir
	playFile := filepath.Join(dir, "inventory", "interzone-"+name+".yml")
	if strings.HasPrefix(play, "#root\n") {
		// Plays that load the playbook's roles must sit next to setup.yml.
		playFile = filepath.Join(dir, "interzone-"+name+".yml")
	}
	if err := os.WriteFile(playFile, []byte(play), 0o644); err != nil {
		return nil, err
	}
	defer os.Remove(playFile)
	cmdArgs := []string{"-i", "inventory/hosts", playFile}
	if len(vars) > 0 {
		f, err := os.CreateTemp(filepath.Join(dir, "inventory"), ".interzone-vars-*.json")
		if err != nil {
			return nil, err
		}
		defer os.Remove(f.Name())
		if err := json.NewEncoder(f).Encode(vars); err != nil {
			f.Close()
			return nil, err
		}
		f.Close()
		cmdArgs = append(cmdArgs, "-e", "@"+f.Name())
	}
	cmd := exec.CommandContext(ctx, bin, append(cmdArgs, args...)...)
	cmd.Dir = dir
	cmd.Env = append(Env(c), "ANSIBLE_STDOUT_CALLBACK=json", "ANSIBLE_CALLBACK_RESULT_FORMAT=json")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, runErr := cmd.Output()

	var doc struct {
		Plays []struct {
			Tasks []struct {
				Task struct {
					Name string `json:"name"`
				} `json:"task"`
				Hosts map[string]TaskResult `json:"hosts"`
			} `json:"tasks"`
		} `json:"plays"`
	}
	if i := bytes.IndexByte(out, '{'); i >= 0 {
		out = out[i:]
	}
	if err := json.Unmarshal(out, &doc); err != nil {
		if runErr != nil {
			return nil, fmt.Errorf("%s", lastLine(stderr.String(), runErr))
		}
		return nil, fmt.Errorf("couldn't read Ansible's output: %w", err)
	}
	res := map[string]TaskResult{}
	for _, p := range doc.Plays {
		for _, t := range p.Tasks {
			for _, h := range t.Hosts {
				if t.Task.Name != "" {
					res[t.Task.Name] = h
				}
				if failed(h) {
					return res, fmt.Errorf("%s: %s", t.Task.Name, taskMessage(h))
				}
			}
		}
	}
	if runErr != nil {
		return res, fmt.Errorf("%s", lastLine(stderr.String(), runErr))
	}
	return res, nil
}

func failed(h TaskResult) bool {
	var b bool
	if json.Unmarshal(h["failed"], &b) == nil && b {
		return true
	}
	return json.Unmarshal(h["unreachable"], &b) == nil && b
}

// taskMessage is the most useful text of a failed task.
func taskMessage(h TaskResult) string {
	for _, k := range []string{"msg", "stderr", "stdout"} {
		if s := strings.TrimSpace(h.String(k)); s != "" {
			return lastLine(s, nil)
		}
	}
	return "failed"
}

func lastLine(s string, fallback error) string {
	s = strings.TrimSpace(s)
	if i := strings.LastIndex(s, "\n"); i >= 0 {
		s = s[i+1:]
	}
	if s == "" && fallback != nil {
		return fallback.Error()
	}
	return s
}
