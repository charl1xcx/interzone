package config

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// interzone can manage several servers. Each one is a profile: a saved
// answers file plus its own copy of the playbook. The first server keeps
// the original config.json; others live in servers/<name>.json. The
// INTERZONE_SERVER environment variable overrides the active one.

// DefaultProfile names the original config.json.
const DefaultProfile = "default"

var profileName = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,39}$`)

// ValidProfileName reports whether name can be used for a new server.
func ValidProfileName(name string) error {
	if !profileName.MatchString(name) {
		return fmt.Errorf("use lowercase letters, digits, dots and dashes (up to 40)")
	}
	return nil
}

func activeFile() string { return filepath.Join(Dir(), "active-server") }

// Active returns the profile the commands and the wizard work on.
func Active() string {
	if p := strings.TrimSpace(os.Getenv("INTERZONE_SERVER")); p != "" {
		return p
	}
	b, err := os.ReadFile(activeFile())
	if p := strings.TrimSpace(string(b)); err == nil && p != "" {
		return p
	}
	return DefaultProfile
}

// SetActive makes name the active profile.
func SetActive(name string) error {
	if err := os.MkdirAll(Dir(), 0o700); err != nil {
		return err
	}
	if name == DefaultProfile {
		err := os.Remove(activeFile())
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	return os.WriteFile(activeFile(), []byte(name+"\n"), 0o600)
}

// ProfilePath is where a profile's answers are saved.
func ProfilePath(name string) string {
	if name == DefaultProfile || name == "" {
		return filepath.Join(Dir(), "config.json")
	}
	return filepath.Join(Dir(), "servers", name+".json")
}

// DataDir is interzone's data directory: playbooks, backups, the venv.
func DataDir() string {
	data, _ := os.UserHomeDir()
	if x := os.Getenv("XDG_DATA_HOME"); x != "" {
		data = x
	} else {
		data = filepath.Join(data, ".local", "share")
	}
	return filepath.Join(data, "interzone")
}

// NewProfile returns fresh answers for another server, with its own playbook
// copy, taking the network settings from base.
func NewProfile(name string, base *Config) *Config {
	c := Default()
	c.PlaybookDir = filepath.Join(DataDir(), "servers", name, "matrix-docker-ansible-deploy")
	if base != nil {
		c.Sources = base.Sources
	}
	return c
}

// Profile is a saved server.
type Profile struct {
	Name   string
	Domain string
	Host   string // "this machine" or user@host
}

// Profiles lists every saved server, the default one first.
func Profiles() []Profile {
	var out []Profile
	add := func(name string) {
		c, err := LoadFile(ProfilePath(name))
		if err != nil {
			return
		}
		p := Profile{Name: name, Domain: c.Domain, Host: "this machine"}
		if c.Target.Mode == "ssh" {
			p.Host = c.Target.User + "@" + c.Target.Host
		}
		out = append(out, p)
	}
	add(DefaultProfile)
	m, _ := filepath.Glob(filepath.Join(Dir(), "servers", "*.json"))
	sort.Strings(m)
	for _, f := range m {
		add(strings.TrimSuffix(filepath.Base(f), ".json"))
	}
	return out
}

// RemoveProfile forgets a saved server. Nothing on the server is touched.
func RemoveProfile(name string) error {
	if name == DefaultProfile {
		return fmt.Errorf("the first server can't be removed, only replaced")
	}
	if name == Active() {
		return fmt.Errorf("switch to another server first")
	}
	return os.Remove(ProfilePath(name))
}
