// Package config holds the user's choices and turns them into the
// inventory files matrix-docker-ansible-deploy expects.
package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"interzone/internal/catalog"
)

const (
	DefaultPlaybookGit    = "https://itsjustadream.g0th.ir/spantaleev/matrix-docker-ansible-deploy"
	DefaultRegistryPrefix = "mechatok.g0th.ir/"
	UpstreamGitHub        = "https://github.com/"
)

type Target struct {
	Mode       string // "local" or "ssh"
	Host       string
	User       string
	Port       string
	SSHKey     string
	BecomePass string
}

type Sources struct {
	PlaybookGit string
	// RolesGitBase replaces https://github.com/ in requirements.yml so Ansible
	// roles come from the same in-country mirror as the playbook.
	RolesGitBase     string
	UseRegistry      bool
	RegistryPrefix   string
	DockerPackageURL string // mirror of https://download.docker.com/linux
	APTMirror        string // mirror for the server's distro packages
	PyPIIndex        string // used when ansible has to be installed locally
	Blackout         bool
}

type SSL struct {
	Mode      string // "letsencrypt", "selfsigned", "custom", "none"
	ACMEEmail string
	CertPath  string
	KeyPath   string
}

type Config struct {
	Domain     string
	Homeserver string
	Target     Target
	Sources    Sources
	SSL        SSL
	IPv6       bool
	Federation bool

	Components map[string]bool
	Values     map[string]string
	// Preset is the ID of the preset the stack was last started from.
	Preset string `json:",omitempty"`

	AdminUser     string
	AdminPassword string
	// AdminToken is the Synapse session interzone's user admin reuses, so
	// it doesn't log in (and hit the login rate limit) for every change.
	AdminToken string
	// CloseRegistration turns off the open registration Conduit, Continuwuity
	// and Tuwunel need for creating the first account.
	CloseRegistration bool

	GenericSecret    string
	PostgresPassword string

	ExtraYAML string
	// Overrides holds role variables changed on the Tuning screen, as raw
	// YAML values keyed by variable name.
	Overrides   map[string]string
	PlaybookDir string

	Backups BackupSchedule
	// PinnedCommit holds the playbook at this commit after a rollback, until
	// the next update.
	PinnedCommit string
}

// BackupSchedule configures the database backups the server makes itself.
type BackupSchedule struct {
	Enabled bool
	When    string // systemd OnCalendar, e.g. "daily" or "*-*-* 03:30"
	Keep    int    // dumps kept on the server
}

func Dir() string {
	if d, err := os.UserConfigDir(); err == nil {
		return filepath.Join(d, "interzone")
	}
	return ".interzone"
}

// Path is where the wizard keeps the active server's answers.
func Path() string { return ProfilePath(Active()) }

func Default() *Config {
	c := &Config{
		Homeserver: "synapse",
		Target:     Target{Mode: "local", User: "root", Port: "22"},
		Sources: Sources{
			PlaybookGit:    DefaultPlaybookGit,
			RolesGitBase:   "https://itsjustadream.g0th.ir/",
			UseRegistry:    true,
			RegistryPrefix: DefaultRegistryPrefix,
		},
		SSL:              SSL{Mode: "letsencrypt"},
		Federation:       true,
		Components:       map[string]bool{},
		Values:           map[string]string{},
		Overrides:        map[string]string{},
		Backups:          BackupSchedule{When: "daily", Keep: 7},
		AdminUser:        "admin",
		AdminPassword:    RandomPassword(20),
		GenericSecret:    RandomPassword(64),
		PostgresPassword: RandomPassword(48),
		PlaybookDir:      filepath.Join(DataDir(), "matrix-docker-ansible-deploy"),
	}
	for _, comp := range catalog.Components {
		if comp.DefaultOn {
			c.Components[comp.ID] = true
		}
	}
	return c
}

// Load returns the saved configuration, or defaults on first run.
func Load() (*Config, bool) {
	c, err := LoadFile(Path())
	if err != nil {
		return Default(), false
	}
	return c, true
}

// LoadFile reads a configuration file written by Save or SaveFile.
func LoadFile(file string) (*Config, error) {
	b, err := os.ReadFile(file)
	if err != nil {
		return nil, err
	}
	c := Default()
	if err := json.Unmarshal(b, c); err != nil {
		return nil, err
	}
	if c.Components == nil {
		c.Components = map[string]bool{}
	}
	if c.Values == nil {
		c.Values = map[string]string{}
	}
	if c.Overrides == nil {
		c.Overrides = map[string]string{}
	}
	return c, nil
}

// Save writes the configuration with owner-only permissions since it holds secrets.
func (c *Config) Save() error { return c.SaveFile(Path()) }

func (c *Config) SaveFile(file string) error {
	if err := os.MkdirAll(filepath.Dir(file), 0o700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(file, b, 0o600)
}

// Installed reports whether a previous run wrote this server's inventory.
func (c *Config) Installed() bool {
	if c.Domain == "" {
		return false
	}
	_, err := os.Stat(filepath.Join(c.PlaybookDir, "inventory", "host_vars", c.HostName(), "vars.yml"))
	return err == nil
}

// RegistrationOpen reports whether the homeserver currently lets anyone
// with the token (or anyone at all, for Conduit) create an account.
func (c *Config) RegistrationOpen() bool {
	switch c.Homeserver {
	case "conduit", "continuwuity", "tuwunel":
		return !c.CloseRegistration
	}
	return false
}

// Enabled reports whether a component is active for the chosen homeserver.
func (c *Config) Enabled(comp catalog.Component) bool {
	if comp.SynapseOnly && c.Homeserver != "synapse" {
		return false
	}
	return c.Components[comp.ID]
}

// FillGenerated creates secrets for enabled components that still lack them.
func (c *Config) FillGenerated() {
	for _, comp := range catalog.Components {
		if !c.Enabled(comp) {
			continue
		}
		for _, f := range comp.Fields {
			if _, ok := c.Values[f.Var]; ok {
				continue
			}
			switch f.Kind {
			case catalog.Secret:
				c.Values[f.Var] = RandomPassword(32)
			case catalog.Hex64:
				c.Values[f.Var] = RandomHex(32)
			default:
				c.Values[f.Var] = f.Default
			}
		}
	}
}

// Missing lists required fields that are still empty, as "Component: Field".
func (c *Config) Missing() []string {
	var out []string
	if c.Domain == "" {
		out = append(out, "Server: base domain")
	}
	if c.Target.Mode == "ssh" && c.Target.Host == "" {
		out = append(out, "Server: SSH host")
	}
	if c.SSL.Mode == "custom" && (c.SSL.CertPath == "" || c.SSL.KeyPath == "") {
		out = append(out, "Server: certificate and key paths")
	}
	for _, comp := range catalog.Components {
		if !c.Enabled(comp) {
			continue
		}
		for _, f := range comp.Fields {
			if f.ShowIf != "" && c.Values[f.ShowIf] != "true" {
				continue
			}
			if f.Required && c.Values[f.Var] == "" {
				out = append(out, comp.Name+": "+f.Label)
			} else if f.Kind == catalog.YAML && c.Values[f.Var] != "" {
				if err := yaml.Unmarshal([]byte(c.Values[f.Var]), new(any)); err != nil {
					out = append(out, comp.Name+": "+f.Label+" isn't valid YAML ("+yamlError(err)+")")
				}
			}
		}
	}
	for _, k := range sortedKeys(c.Overrides) {
		if err := yaml.Unmarshal([]byte(c.Overrides[k]), new(any)); err != nil {
			out = append(out, "Fine-tune: "+k+" isn't valid YAML ("+yamlError(err)+")")
		}
	}
	if extra := strings.TrimSpace(c.ExtraYAML); extra != "" {
		var m map[string]any
		if err := yaml.Unmarshal([]byte(extra), &m); err != nil {
			out = append(out, "Components: extra vars.yml lines aren't valid YAML ("+yamlError(err)+")")
		}
	}
	return out
}

// yamlError shortens yaml.v3's messages, e.g. "line 2: could not find expected ':'".
func yamlError(err error) string {
	if _, ok := err.(*yaml.TypeError); ok {
		return "expected lines like key: value"
	}
	return strings.TrimPrefix(err.Error(), "yaml: ")
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
