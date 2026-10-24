// Package config holds the user's choices and turns them into the
// inventory files matrix-docker-ansible-deploy expects.
package config

import (
	"encoding/json"
	"os"
	"path/filepath"

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

	AdminUser     string
	AdminPassword string

	GenericSecret    string
	PostgresPassword string

	ExtraYAML   string
	PlaybookDir string
}

func Dir() string {
	if d, err := os.UserConfigDir(); err == nil {
		return filepath.Join(d, "interzone")
	}
	return ".interzone"
}

func path() string { return filepath.Join(Dir(), "config.json") }

func Default() *Config {
	data, _ := os.UserHomeDir()
	if x := os.Getenv("XDG_DATA_HOME"); x != "" {
		data = x
	} else {
		data = filepath.Join(data, ".local", "share")
	}
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
		AdminUser:        "admin",
		AdminPassword:    RandomPassword(20),
		GenericSecret:    RandomPassword(64),
		PostgresPassword: RandomPassword(48),
		PlaybookDir:      filepath.Join(data, "interzone", "matrix-docker-ansible-deploy"),
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
	b, err := os.ReadFile(path())
	if err != nil {
		return Default(), false
	}
	c := Default()
	if err := json.Unmarshal(b, c); err != nil {
		return Default(), false
	}
	if c.Components == nil {
		c.Components = map[string]bool{}
	}
	if c.Values == nil {
		c.Values = map[string]string{}
	}
	return c, true
}

// Save writes the configuration with owner-only permissions since it holds secrets.
func (c *Config) Save() error {
	if err := os.MkdirAll(Dir(), 0o700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path(), b, 0o600)
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
		out = append(out, "SSL: certificate and key paths")
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
			}
		}
	}
	return out
}
