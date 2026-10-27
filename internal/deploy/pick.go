package deploy

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"interzone/internal/config"
)

// In blackout mode a run picks its mirrors itself: the one set on the
// Server step if it carries what this server needs, otherwise the first
// known mirror that does. A pick that changes anything is saved back
// through a Sources event.

// candidates is current followed by the known URLs, without repeats.
func candidates(current string, known []string) []string {
	out := []string{}
	for _, u := range append([]string{current}, known...) {
		if u != "" && !slices.Contains(out, u) {
			out = append(out, u)
		}
	}
	return out
}

func (r *Runner) picked(step Step, what, from, to string) {
	if from == to {
		return
	}
	if from == "" {
		r.note(step, what+": using "+to)
	} else {
		r.note(step, fmt.Sprintf("%s: %s doesn't carry everything this server needs; using %s", what, from, to))
	}
	src := r.cfg.Sources
	r.emit(Event{Step: step, Sources: &src})
}

func orNone(s string) string {
	if s == "" {
		return "no mirror"
	}
	return s
}

// pickPyPI picks the PyPI index Ansible is installed from.
func (r *Runner) pickPyPI(ctx context.Context) {
	if !r.cfg.Sources.Blackout {
		return
	}
	cur := r.cfg.Sources.PyPIIndex
	var known []string
	for _, m := range config.Mirrors(config.MirrorPyPI) {
		known = append(known, m.URL)
	}
	for _, u := range candidates(cur, known) {
		if _, err := ProbeMirror(ctx, config.MirrorPyPI, u); err == nil {
			r.cfg.Sources.PyPIIndex = u
			r.env = Env(r.cfg)
			r.picked(StepAnsible, "PyPI", cur, u)
			return
		}
	}
	r.note(StepAnsible, "! no known PyPI mirror lists ansible-core; trying "+orNone(cur))
}

// pickPackages picks the OS and Docker package mirrors for the server's
// distro and release.
func (r *Runner) pickPackages(ctx context.Context) error {
	facts, err := probe(ctx, r.cfg)
	if err != nil {
		r.note(StepMirrors, "! couldn't look at the server to pick mirrors: "+err.Error())
		return nil
	}
	osNeeds, docker, why := serverNeeds(facts)
	if docker != nil && facts["docker"] != "" {
		docker = nil // installed already
	}
	src := &r.cfg.Sources
	if why == "" && len(osNeeds) > 0 {
		r.pickFor(ctx, config.MirrorOS, &src.APTMirror, osNeeds, facts["arch"])
	}
	if docker != nil {
		was := src.DockerPackageURL
		r.pickFor(ctx, config.MirrorDocker, &src.DockerPackageURL, []need{*docker}, facts["arch"])
		if src.DockerPackageURL != was {
			return r.writeInventory(StepMirrors) // vars.yml has docker_repo_url
		}
	}
	return nil
}

// pickFor sets *url to the first candidate mirror of kind carrying every
// release in needs.
func (r *Runner) pickFor(ctx context.Context, kind config.MirrorKind, url *string, needs []need, arch string) {
	what := map[config.MirrorKind]string{config.MirrorOS: "OS packages", config.MirrorDocker: "Docker packages"}[kind]
	var known []string
	for _, m := range config.Mirrors(kind) {
		if slices.ContainsFunc(needs, func(n need) bool { return !slices.Contains(m.Covers, n.distro) }) {
			continue
		}
		known = append(known, m.URL)
	}
	var names []string
	for _, n := range needs {
		names = append(names, name(n.distro, n.version))
	}
	for _, u := range candidates(*url, known) {
		ok := !slices.ContainsFunc(needs, func(n need) bool {
			_, err := ProbeDistro(ctx, kind, u, n.distro, n.version, arch)
			return err != nil
		})
		if ok {
			cur := *url
			*url = u
			r.picked(StepMirrors, what, cur, u)
			r.note(StepMirrors, what+": "+u+" carries "+strings.Join(names, ", "))
			return
		}
	}
	r.note(StepMirrors, "! "+what+": no known mirror carries "+strings.Join(names, ", ")+"; keeping "+orNone(*url))
}

// pickRegistry checks the images against the registry set on the Server
// step and then the known ones, and keeps the first that has all of them,
// or else the one missing the fewest. imgs are named as vars.yml has them,
// under the current registry prefix.
func (r *Runner) pickRegistry(ctx context.Context, imgs []Image, arch string) []ImageStatus {
	src := &r.cfg.Sources
	cur := strings.TrimPrefix(strings.TrimPrefix(src.RegistryPrefix, "https://"), "http://")
	cur = strings.TrimRight(cur, "/") + "/"
	var best []ImageStatus
	bestMissing, bestAt := len(imgs)+1, ""
	for _, reg := range candidates(cur, config.KnownRegistries) {
		moved := make([]Image, len(imgs))
		for i, im := range imgs {
			moved[i] = im
			moved[i].Ref = reg + strings.TrimPrefix(im.Ref, cur)
		}
		st := CheckImages(ctx, moved, arch)
		missing := 0
		for _, s := range st {
			if !s.OK {
				missing++
			}
		}
		r.note(StepImages, fmt.Sprintf("%s has %d of %d images", reg, len(imgs)-missing, len(imgs)))
		if missing < bestMissing {
			best, bestMissing, bestAt = st, missing, reg
		}
		if missing == 0 {
			break
		}
	}
	if bestAt != cur {
		src.RegistryPrefix = bestAt
		r.picked(StepImages, "Container images", cur, bestAt)
		if err := r.writeInventory(StepImages); err != nil {
			r.note(StepImages, "! "+err.Error())
		}
	}
	return best
}
