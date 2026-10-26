package config

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"interzone/internal/catalog"
)

// ── Presets ────────────────────────────────────────────────────────
//
// A preset is a homeserver and a set of components to start from. The
// built-in ones come from the catalog; presets.json holds the user's own,
// and their versions of built-in ones, for every server.

type Preset struct {
	catalog.Preset
	Builtin bool `json:"-"` // one of interzone's
	Edited  bool `json:"-"` // a built-in one the user saved over
}

func presetsPath() string { return filepath.Join(Dir(), "presets.json") }

func userPresets() ([]catalog.Preset, error) {
	b, err := os.ReadFile(presetsPath())
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	} else if err != nil {
		return nil, err
	}
	var ps []catalog.Preset
	if err := json.Unmarshal(b, &ps); err != nil {
		return nil, errors.New(presetsPath() + ": " + err.Error())
	}
	return ps, nil
}

func writePresets(ps []catalog.Preset) error {
	if err := os.MkdirAll(Dir(), 0o700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(ps, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(presetsPath(), b, 0o600)
}

// Presets lists the built-in presets, as the user changed them, then the
// user's own. A broken presets.json leaves only the built-in ones.
func Presets() ([]Preset, error) {
	user, err := userPresets()
	var out []Preset
	for _, b := range catalog.Presets {
		p := Preset{Preset: b, Builtin: true}
		if i := slices.IndexFunc(user, func(u catalog.Preset) bool { return u.ID == b.ID }); i >= 0 {
			p.Preset, p.Edited = user[i], true
		}
		out = append(out, p)
	}
	for _, u := range user {
		if !slices.ContainsFunc(catalog.Presets, func(b catalog.Preset) bool { return b.ID == u.ID }) {
			out = append(out, Preset{Preset: u})
		}
	}
	return out, err
}

// PresetNamed finds a preset by its name, ignoring case.
func PresetNamed(name string) (Preset, bool) {
	ps, _ := Presets()
	for _, p := range ps {
		if strings.EqualFold(strings.TrimSpace(p.Name), strings.TrimSpace(name)) {
			return p, true
		}
	}
	return Preset{}, false
}

// SavePreset stores p, replacing the preset with the same name (a built-in
// one is kept underneath, for DeletePreset to bring back), and returns it
// with its ID.
func SavePreset(p catalog.Preset) (catalog.Preset, error) {
	p.Name = strings.TrimSpace(p.Name)
	if p.Name == "" {
		return p, errors.New("a preset needs a name")
	}
	user, err := userPresets()
	if err != nil {
		return p, err
	}
	if old, ok := PresetNamed(p.Name); ok {
		p.ID = old.ID
	} else {
		p.ID = presetID(p.Name)
	}
	if i := slices.IndexFunc(user, func(u catalog.Preset) bool { return u.ID == p.ID }); i >= 0 {
		user[i] = p
	} else {
		user = append(user, p)
	}
	return p, writePresets(user)
}

// DeletePreset removes one of the user's presets, or brings a built-in one
// back to how interzone ships it.
func DeletePreset(id string) error {
	user, err := userPresets()
	if err != nil {
		return err
	}
	return writePresets(slices.DeleteFunc(user, func(u catalog.Preset) bool { return u.ID == id }))
}

// presetID makes an ID from a name that no other preset uses.
func presetID(name string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(name) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case b.Len() > 0 && !strings.HasSuffix(b.String(), "-"):
			b.WriteByte('-')
		}
	}
	base := strings.Trim(b.String(), "-")
	if base == "" {
		base = "preset"
	}
	ps, _ := Presets()
	taken := func(id string) bool { return slices.ContainsFunc(ps, func(p Preset) bool { return p.ID == id }) }
	id := base
	for n := 2; taken(id); n++ {
		id = base + "-" + strconv.Itoa(n)
	}
	return id
}

// ApplyPreset sets the homeserver, makes the ticked components exactly the
// preset's and opens or closes sign-up as it says. Settings already filled
// in stay, for if they're ticked again.
func (c *Config) ApplyPreset(p catalog.Preset) {
	c.Homeserver = p.Homeserver
	c.Components = map[string]bool{}
	for _, comp := range catalog.Components {
		if slices.Contains(p.Components, comp.ID) && (!comp.SynapseOnly || p.Homeserver == "synapse") {
			c.Components[comp.ID] = true
		}
	}
	c.Signup = p.Signup
	c.Preset = p.ID
	c.FillGenerated()
}

// Stack is the current homeserver and components, as a preset.
func (c *Config) Stack() catalog.Preset {
	p := catalog.Preset{Homeserver: c.Homeserver}
	for _, comp := range catalog.Components {
		if c.Components[comp.ID] && (!comp.SynapseOnly || c.Homeserver == "synapse") {
			p.Components = append(p.Components, comp.ID)
		}
	}
	return p
}

// Matches reports whether c has exactly p's homeserver and components.
func (p Preset) Matches(c *Config) bool {
	s := c.Stack()
	if s.Homeserver != p.Homeserver || len(s.Components) != len(p.Components) {
		return false
	}
	for _, id := range s.Components {
		if !slices.Contains(p.Components, id) {
			return false
		}
	}
	return true
}
