// Package config holds the user's choices and turns them into the
// inventory files matrix-docker-ansible-deploy expects.
package config

import (
	"encoding/json"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"

	"interzone/internal/catalog"
)

const (
	DefaultPlaybookGit    = "https://itsjustadream.g0th.ir/spantaleev/matrix-docker-ansible-deploy"
	DefaultRegistryPrefix = "mechatok.g0th.ir/"
	DefaultRolesGitBase   = "https://itsjustadream.g0th.ir/"
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
	// FederateOnly, when set, are the only servers federated with;
	// FederateNever are never federated with (not on Synapse).
	FederateOnly, FederateNever []string

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
	// Signup lets anyone with SignupToken create an account through MAS,
	// without an email address. It's off unless a preset or Manage turns
	// it on; the token is made on the server by each install.
	Signup      bool   `json:",omitempty"`
	SignupToken string `json:",omitempty"`

	GenericSecret    string
	PostgresPassword string

	ExtraYAML string
	// Overrides holds role variables changed on the Tuning screen, as raw
	// YAML values keyed by variable name.
	Overrides   map[string]string
	PlaybookDir string

	Backups BackupSchedule
	Alerts  Alerts
	// PinnedCommit holds the playbook at this commit after a rollback, until
	// the next update.
	PinnedCommit string
}

// Alerts configures the checks the server runs on itself, sending a
// message when something breaks and when it's fine again.
type Alerts struct {
	Enabled bool
	URL     string // where messages are POSTed
	JSON    bool   // {"text": …} for webhooks (Hookshot, Slack, Discord), else plain text (ntfy)
	Every   string // systemd OnCalendar, e.g. "*:0/5"
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
			RolesGitBase:   DefaultRolesGitBase,
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
		if comp.DefaultOn && !comp.StartOff {
			c.Components[comp.ID] = true
		}
	}
	return c
}

// Fresh is Default for a server being set up from scratch: Synapse with
// the Matrix Authentication Service, which the wizard turns on unless it's
// unticked. Default itself stays without it, as saved answers are read on
// top of it.
func Fresh() *Config {
	c := Default()
	c.Components["mas"] = true
	return c
}

// Load returns the saved configuration, or fresh answers on first run.
func Load() (*Config, bool) {
	c, err := LoadFile(Path())
	if err != nil {
		return Fresh(), false
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
	c.RenameOld(c.PlaybookDir)
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
	case "synapse":
		return c.Signup && c.UsesMAS()
	}
	return false
}

// UsesMAS reports whether the server's accounts live in MAS: ticked, or
// turned on in Fine-tune.
// FederationPort is where other servers reach this one. Conduit and its
// forks serve federation on 443 next to the client API.
func (c *Config) FederationPort() string {
	switch c.Homeserver {
	case "conduit", "continuwuity", "tuwunel":
		return "443"
	}
	return "8448"
}

func (c *Config) UsesMAS() bool {
	return c.Homeserver == "synapse" && (c.Components["mas"] || c.Overrides["matrix_authentication_service_enabled"] == "true")
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
	if c.Signup && c.SignupToken == "" {
		c.SignupToken = RandomPassword(20)
	}
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

var hostname = regexp.MustCompile(`^([a-z0-9]([a-z0-9-]*[a-z0-9])?\.)+[a-z0-9-]*[a-z]$`)

// ValidHost reports whether h is a plain lowercase hostname like chat.example.com.
func ValidHost(h string) bool { return len(h) <= 253 && hostname.MatchString(h) }

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
		if h := c.Values[comp.HostVar()]; h != "" && !ValidHost(h) {
			out = append(out, comp.Name+": address "+h+" isn't a hostname")
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
	for _, k := range slices.Sorted(maps.Keys(c.Overrides)) {
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
