package config

import "fmt"

// Deployed is what the server was installed or imported as: the choices
// that can't change afterwards without losing what's on it.
type Deployed struct {
	Domain, Homeserver string
	MAS                bool
}

// MarkDeployed records the current choices as the ones the server runs.
func (c *Config) MarkDeployed() {
	c.Deployed = &Deployed{Domain: c.Domain, Homeserver: c.Homeserver, MAS: c.Components["mas"]}
}

// Locked says why a setup choice can't change any more, or "": what is
// "domain", "homeserver" or a component's ID.
func (c *Config) Locked(what string) string {
	d := c.Deployed
	if d == nil {
		return ""
	}
	switch what {
	case "domain":
		return "Every user and room ID ends in :" + d.Domain + ", so a server can't be renamed. A new domain is a new server: add one under Servers."
	case "homeserver":
		return "A running server can't switch homeservers: none reads another's database, and its users and rooms would be gone. Another homeserver is a new server: add one under Servers."
	case "mas":
		if d.Homeserver != "synapse" {
			return ""
		}
		if d.MAS {
			return "Accounts and sessions live in MAS on this server; turning it off would lock everyone out."
		}
		return "Moving an installed Synapse's accounts to MAS is a migration (syn2mas); turning it on alone would lock everyone out. Follow the playbook's guide, docs/configuring-playbook-matrix-authentication-service.md, then import the server again."
	}
	return ""
}

// Breaks lists the locked choices that differ from what the server runs;
// a deploy with any of them would ruin it.
func (c *Config) Breaks() []string {
	d := c.Deployed
	if d == nil {
		return nil
	}
	var out []string
	if c.Domain != d.Domain {
		out = append(out, fmt.Sprintf("the domain is %s, but the server runs as %s", c.Domain, d.Domain))
	}
	if c.Homeserver != d.Homeserver {
		out = append(out, fmt.Sprintf("the homeserver is %s, but the server runs %s", c.Homeserver, d.Homeserver))
	}
	if d.Homeserver == "synapse" && c.Homeserver == "synapse" && c.Components["mas"] != d.MAS {
		out = append(out, map[bool]string{true: "MAS was turned off", false: "MAS was turned on"}[d.MAS])
	}
	return out
}

// KeepDeployed puts the locked choices back, after a preset replaced the
// stack.
func (c *Config) KeepDeployed() {
	if d := c.Deployed; d != nil {
		c.Domain, c.Homeserver = d.Domain, d.Homeserver
		if d.Homeserver == "synapse" {
			c.Components["mas"] = d.MAS
		}
	}
}
