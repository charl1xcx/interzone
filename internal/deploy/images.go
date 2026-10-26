package deploy

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"gopkg.in/yaml.v3"

	"interzone/internal/config"
)

// The image pre-check asks Ansible which images the enabled services use
// (it evaluates the playbook's own variables, so it sees exactly what a
// deploy would pull) and then asks each registry whether it has them.

// imagesTask lists every *_container_image variable (and the _to_use and
// _final variants some roles pick the image with), and whether its
// service is enabled. A variable belongs to the longest prefix of its name that has
// an _enabled flag, e.g. matrix_synapse_admin_container_image to
// matrix_synapse_admin_enabled.
const imagesTask = `
- name: interzone facts
  tags: [interzone-images]
  ansible.builtin.setup:
    gather_subset: ["!all", "!min", "platform"]

- name: interzone images
  tags: [interzone-images]
  ansible.builtin.debug:
    msg:
      arch: "{{ ansible_architecture }}"
      images: "{{ interzone_images | to_json }}"
  vars:
    interzone_images: |-
      {%- set enabled = query('ansible.builtin.varnames', '_enabled$') -%}
      {%- set ns = namespace(out=[]) -%}
      {%- for name in query('ansible.builtin.varnames', '^[a-z0-9_]+_container_image(_to_use|_final)?$') | sort -%}
        {%- set parts = (name | regex_replace('_container_image(_to_use|_final)?$', '')).split('_') -%}
        {%- set st = namespace(found=false, on=false) -%}
        {%- for i in range(parts | length, 0, -1) -%}
          {%- set flag = (parts[:i] | join('_')) ~ '_enabled' -%}
          {%- if not st.found and flag in enabled -%}
            {%- set st.found = true -%}
            {%- set st.on = lookup('ansible.builtin.vars', flag) | bool -%}
          {%- endif -%}
        {%- endfor -%}
        {%- set ns.out = ns.out + [{'var': name, 'image': lookup('ansible.builtin.vars', name) | string | trim, 'on': st.on}] -%}
      {%- endfor -%}
      {{ ns.out }}
`

// imagesPlay is setup.yml's play (so every role's defaults load) with all
// its tasks replaced by imagesTask.
func imagesPlay(playbookDir string) (string, error) {
	b, err := os.ReadFile(filepath.Join(playbookDir, "setup.yml"))
	if err != nil {
		return "", err
	}
	var plays []map[string]any
	if err := yaml.Unmarshal(b, &plays); err != nil || len(plays) == 0 {
		return "", fmt.Errorf("couldn't read setup.yml: %v", err)
	}
	var tasks []any
	if err := yaml.Unmarshal([]byte(imagesTask), &tasks); err != nil {
		return "", err
	}
	p := plays[0]
	for _, k := range []string{"pre_tasks", "tasks", "post_tasks", "handlers"} {
		delete(p, k)
	}
	// Fact gathering is tagged "always", which the run skips; imagesTask
	// gathers the one fact it needs itself.
	p["gather_facts"] = false
	p["post_tasks"] = tasks
	out, err := yaml.Marshal([]any{p})
	return "#root\n" + string(out), err
}

// Image is a container image the playbook can deploy.
type Image struct {
	Var    string `json:"var"`
	Ref    string `json:"image"`
	Needed bool   `json:"on"` // an enabled service uses it
}

// ListImages returns the images the server's enabled services use, or with
// all every image the playbook knows, and the server's CPU architecture.
func ListImages(ctx context.Context, c *config.Config, all bool) ([]Image, string, error) {
	play, err := imagesPlay(c.PlaybookDir)
	if err != nil {
		return nil, "", err
	}
	res, err := runPlayJSON(ctx, c, "images", play, nil, "--tags=interzone-images", "--skip-tags=always")
	if err != nil {
		return nil, "", err
	}
	t, ok := res["interzone images"]
	if !ok {
		return nil, "", fmt.Errorf("the playbook didn't report its images")
	}
	var msg struct {
		Arch   string          `json:"arch"`
		Images json.RawMessage `json:"images"`
	}
	if err := json.Unmarshal(t["msg"], &msg); err != nil {
		return nil, "", err
	}
	var imgs []Image
	if err := json.Unmarshal(msg.Images, &imgs); err != nil {
		// Ansible may hand the JSON back as a string.
		var s string
		if json.Unmarshal(msg.Images, &s) != nil || json.Unmarshal([]byte(s), &imgs) != nil {
			return nil, "", fmt.Errorf("couldn't read the image list")
		}
	}
	seen := map[string]int{}
	var out []Image
	for _, im := range imgs {
		// Locally built images never come from a registry.
		if im.Ref == "" || strings.HasPrefix(im.Ref, "localhost/") || !im.Needed && !all {
			continue
		}
		if i, ok := seen[im.Ref]; ok { // two services sharing an image
			out[i].Needed = out[i].Needed || im.Needed
			continue
		}
		seen[im.Ref] = len(out)
		out = append(out, im)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Ref < out[j].Ref })
	return out, msg.Arch, nil
}

// ImageStatus is whether a registry has an image.
type ImageStatus struct {
	Image
	OK     bool
	Detail string // why not
}

// CheckImages asks each image's registry for its manifest. Multi-arch
// images must include the server's architecture.
func CheckImages(ctx context.Context, imgs []Image, arch string) []ImageStatus {
	out := make([]ImageStatus, len(imgs))
	sem := make(chan struct{}, 8)
	var wg sync.WaitGroup
	cl := &registryClient{http: &http.Client{Timeout: 20 * time.Second}, tokens: map[string]string{}}
	for i, im := range imgs {
		wg.Add(1)
		go func(i int, im Image) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			st := ImageStatus{Image: im, OK: true}
			if err := cl.has(ctx, im.Ref, ociArch(arch)); err != nil {
				st.OK, st.Detail = false, err.Error()
			}
			out[i] = st
		}(i, im)
	}
	wg.Wait()
	return out
}

func ociArch(a string) string {
	switch a {
	case "x86_64":
		return "amd64"
	case "aarch64":
		return "arm64"
	case "armv7l":
		return "arm"
	}
	return a
}

// ParseRef splits an image reference into registry, repository and tag
// (or digest). Docker Hub shorthand is expanded.
func ParseRef(ref string) (host, repo, tag string) {
	name := ref
	if i := strings.Index(name, "@"); i >= 0 {
		name, tag = name[:i], name[i+1:]
	} else if i := strings.LastIndex(name, ":"); i > strings.LastIndex(name, "/") {
		name, tag = name[:i], name[i+1:]
	}
	if tag == "" {
		tag = "latest"
	}
	first, rest, ok := strings.Cut(name, "/")
	if ok && (strings.ContainsAny(first, ".:") || first == "localhost") {
		host, repo = first, rest
	} else {
		host, repo = "registry-1.docker.io", name
		if !ok {
			repo = "library/" + name
		}
	}
	if host == "docker.io" {
		host = "registry-1.docker.io"
	}
	return host, repo, tag
}

type registryClient struct {
	http   *http.Client
	mu     sync.Mutex
	tokens map[string]string // realm+scope → bearer token
}

const manifestTypes = "application/vnd.oci.image.index.v1+json, application/vnd.docker.distribution.manifest.list.v2+json, " +
	"application/vnd.oci.image.manifest.v1+json, application/vnd.docker.distribution.manifest.v2+json"

var authParam = regexp.MustCompile(`(\w+)="([^"]*)"`)

// has reports nil if the registry serves ref for arch.
func (c *registryClient) has(ctx context.Context, ref, arch string) error {
	host, repo, tag := ParseRef(ref)
	url := "https://" + host + "/v2/" + repo + "/manifests/" + tag
	resp, err := c.get(ctx, url, "")
	if err != nil {
		return err
	}
	if resp.StatusCode == http.StatusUnauthorized {
		token, err := c.token(ctx, resp.Header.Get("WWW-Authenticate"))
		resp.Body.Close()
		if err != nil {
			return err
		}
		if resp, err = c.get(ctx, url, token); err != nil {
			return err
		}
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusNotFound:
		return fmt.Errorf("not in the registry")
	case http.StatusUnauthorized, http.StatusForbidden:
		return fmt.Errorf("the registry refused access (%d)", resp.StatusCode)
	default:
		return fmt.Errorf("the registry answered %s", resp.Status)
	}
	var idx struct {
		Manifests []struct {
			Platform struct {
				Architecture string `json:"architecture"`
			} `json:"platform"`
		} `json:"manifests"`
	}
	if json.NewDecoder(resp.Body).Decode(&idx) != nil || len(idx.Manifests) == 0 || arch == "" {
		return nil // a single-architecture image: nothing more to check
	}
	var archs []string
	for _, m := range idx.Manifests {
		if m.Platform.Architecture == arch {
			return nil
		}
		if a := m.Platform.Architecture; a != "" && a != "unknown" {
			archs = append(archs, a)
		}
	}
	return fmt.Errorf("no %s build (only %s)", arch, strings.Join(archs, ", "))
}

func (c *registryClient) get(ctx context.Context, url, token string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", manifestTypes)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("can't reach the registry: %s", shortNetErr(err))
	}
	return resp, nil
}

// token fetches an anonymous pull token for a Bearer challenge.
func (c *registryClient) token(ctx context.Context, challenge string) (string, error) {
	if !strings.HasPrefix(challenge, "Bearer ") {
		return "", fmt.Errorf("the registry needs a login")
	}
	p := map[string]string{}
	for _, m := range authParam.FindAllStringSubmatch(challenge, -1) {
		p[m[1]] = m[2]
	}
	key := p["realm"] + "|" + p["service"] + "|" + p["scope"]
	c.mu.Lock()
	t, ok := c.tokens[key]
	c.mu.Unlock()
	if ok {
		return t, nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p["realm"], nil)
	if err != nil {
		return "", err
	}
	q := req.URL.Query()
	for _, k := range []string{"service", "scope"} {
		if p[k] != "" {
			q.Set(k, p[k])
		}
	}
	req.URL.RawQuery = q.Encode()
	resp, err := c.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("can't reach the registry's login: %s", shortNetErr(err))
	}
	defer resp.Body.Close()
	var v struct {
		Token       string `json:"token"`
		AccessToken string `json:"access_token"`
	}
	if resp.StatusCode != http.StatusOK || json.NewDecoder(resp.Body).Decode(&v) != nil {
		return "", fmt.Errorf("the registry refused an anonymous login (%s)", resp.Status)
	}
	t = v.Token
	if t == "" {
		t = v.AccessToken
	}
	c.mu.Lock()
	c.tokens[key] = t
	c.mu.Unlock()
	return t, nil
}

func shortNetErr(err error) string {
	s := err.Error()
	if i := strings.LastIndex(s, ": "); i >= 0 {
		return s[i+2:]
	}
	return s
}

// images is the pre-deploy step. Missing images are a warning, not a
// failure: the server may already have them.
func (r *Runner) images(ctx context.Context) error {
	r.begin(StepImages)
	r.note(StepImages, "asking the playbook which images the enabled services use…")
	imgs, arch, err := ListImages(ctx, r.cfg, false)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if r.opts.Action == ActionImages {
			return err
		}
		r.note(StepImages, "! couldn't list the images: "+err.Error())
		r.emit(Event{Step: StepImages, Warn: true})
		return nil
	}
	r.note(StepImages, fmt.Sprintf("%d images for %s; checking the registries…", len(imgs), arch))
	for _, st := range CheckImages(ctx, imgs, arch) {
		if st.OK {
			r.result.Images = append(r.result.Images, st.Ref)
			r.emit(Event{Step: StepImages, Line: "ok: " + st.Ref})
			continue
		}
		r.result.MissingImages = append(r.result.MissingImages, st.Ref+": "+st.Detail)
		r.emit(Event{Step: StepImages, Line: "missing: " + st.Ref + " (" + st.Detail + ")", Failed: true})
	}
	if n := len(r.result.MissingImages); n > 0 {
		r.note(StepImages, fmt.Sprintf("! %d of %d images can't be pulled; the deploy fails when it reaches them unless the server already has them", n, len(imgs)))
		r.emit(Event{Step: StepImages, Warn: true})
	}
	return nil
}
