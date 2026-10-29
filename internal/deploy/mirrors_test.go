package deploy

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"interzone/internal/config"
)

// TestKnownMirrors checks every mirror in the catalog over the network:
// each must carry a fresh copy of every release in RefVersions of every
// distro it claims to cover. It is skipped unless INTERZONE_MIRROR_TESTS=1,
// since it needs to reach servers in Iran:
//
//	INTERZONE_MIRROR_TESTS=1 go test ./internal/deploy -run KnownMirrors -v
func TestKnownMirrors(t *testing.T) {
	if os.Getenv("INTERZONE_MIRROR_TESTS") == "" {
		t.Skip("set INTERZONE_MIRROR_TESTS=1 to probe the known mirrors")
	}
	for _, m := range config.KnownMirrors {
		t.Run(string(m.Kind)+"/"+m.Name, func(t *testing.T) {
			t.Parallel()
			ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
			defer cancel()
			if m.Kind == config.MirrorPyPI {
				detail, err := ProbeMirror(ctx, m.Kind, m.URL)
				if err != nil {
					t.Fatalf("%s: %v", m.URL, err)
				}
				t.Logf("%s: %s", m.URL, detail)
				return
			}
			for _, d := range m.Covers {
				for _, v := range RefVersions[m.Kind][d] {
					age, err := ProbeDistro(ctx, m.Kind, m.URL, d, v, "x86_64")
					if err != nil {
						t.Errorf("%s: %s: %v", m.URL, name(d, v), err)
						continue
					}
					t.Logf("%s: %s, %s old", m.URL, name(d, v), days(age))
				}
			}
		})
	}
}

func TestCatalog(t *testing.T) {
	for _, k := range []config.MirrorKind{config.MirrorOS, config.MirrorDocker, config.MirrorPyPI} {
		if config.DefaultMirror(k) == "" {
			t.Errorf("no default %s mirror", k)
		}
	}
	for _, m := range config.KnownMirrors {
		for _, d := range m.Covers {
			if _, ok := RefVersions[m.Kind][d]; !ok {
				t.Errorf("%s %s covers %q, which has no reference versions", m.Kind, m.Name, d)
			}
		}
	}
	// Every distro that installs Docker from download.docker.com should have
	// some known Docker mirror.
	for d := range RefVersions[config.MirrorDocker] {
		if len(config.MirrorsFor(config.MirrorDocker, d)) == 0 {
			t.Errorf("no known Docker mirror covers %s", d)
		}
	}
}

func TestProbeDistro(t *testing.T) {
	fresh := time.Now().Add(-30 * time.Hour)
	stale := time.Now().Add(-60 * 24 * time.Hour)
	release := func(t time.Time) string { return "Suite: x\nDate: " + t.UTC().Format(time.RFC1123) + "\n" }
	repomd := func(t time.Time) string {
		return "<repomd><revision>" + itoa(t.Unix()) + "</revision></repomd>"
	}
	files := map[string]string{
		"/m/ubuntu/dists/noble-security/Release":                 release(fresh),
		"/m/ubuntu/dists/jammy-security/Release":                 release(fresh),
		"/m/debian-security/dists/trixie-security/Release":       release(stale),
		"/m/almalinux/9/BaseOS/x86_64/os/repodata/repomd.xml":    repomd(fresh),
		"/m/epel/9/Everything/x86_64/repodata/repomd.xml":        repomd(stale),
		"/m/archlinux/core/os/x86_64/core.db":                    "binary",
		"/d/centos/9/x86_64/stable/repodata/repomd.xml":          repomd(stale),
		"/d/ubuntu/dists/noble/Release":                          release(stale),
		"/p/ansible-core/":                                       `<a href="ansible_core-2.19.9.tar.gz">x</a><a href="ansible_core-2.19.14-py3-none-any.whl">x</a><a href="ansible-core-2.9.1.tar.gz">x</a>`,
		"/e/ansible-core/":                                       "<html></html>",
		"/m/fedora/linux/updates/44/Everything/x86_64/repodata/": "",
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, ok := files[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		if strings.HasSuffix(r.URL.Path, ".db") {
			w.Header().Set("Last-Modified", fresh.UTC().Format(http.TimeFormat))
		}
		w.Write([]byte(body))
	}))
	defer srv.Close()

	for _, tc := range []struct {
		kind              config.MirrorKind
		base, distro, ver string
		err               string // substring of the error, or "" for success
	}{
		{config.MirrorOS, "/m", config.Ubuntu, "noble", ""},
		{config.MirrorOS, "/m", config.Debian, "trixie", "Debian trixie is 60 days old"},
		{config.MirrorOS, "/m", config.AlmaLinux, "9", ""},
		{config.MirrorOS, "/m", config.AlmaLinux, "10", "HTTP 404"},
		{config.MirrorOS, "/m", config.EPEL, "9", "EPEL 9 is 60 days old"},
		{config.MirrorOS, "/m", config.Arch, "", ""},
		{config.MirrorOS, "/m", config.Fedora, "44", "HTTP 404"},
		{config.MirrorDocker, "/d", config.CentOS, "9", ""}, // Docker indexes may be old
		{config.MirrorDocker, "/d", config.Ubuntu, "noble", ""},
		{config.MirrorDocker, "/d", config.Fedora, "44", "HTTP 404"},
	} {
		_, err := ProbeDistro(context.Background(), tc.kind, srv.URL+tc.base, tc.distro, tc.ver, "x86_64")
		switch {
		case tc.err == "" && err != nil:
			t.Errorf("%s %s %s: %v", tc.kind, tc.distro, tc.ver, err)
		case tc.err != "" && (err == nil || !strings.Contains(err.Error(), tc.err)):
			t.Errorf("%s %s %s: got %v, want error containing %q", tc.kind, tc.distro, tc.ver, err, tc.err)
		}
	}

	ctx := context.Background()
	if got, err := ProbeMirror(ctx, config.MirrorOS, srv.URL+"/m"); err != nil || got != "carries Ubuntu, Arch Linux" {
		t.Errorf("ProbeMirror(os) = %q, %v", got, err)
	}
	if got, err := ProbeMirror(ctx, config.MirrorPyPI, srv.URL+"/p"); err != nil || got != "ansible-core 2.19.14" {
		t.Errorf("ProbeMirror(pypi) = %q, %v", got, err)
	}
	if _, err := ProbeMirror(ctx, config.MirrorPyPI, srv.URL+"/e"); err == nil {
		t.Errorf("ProbeMirror(pypi) on an empty index succeeded")
	}
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }

func TestServerNeeds(t *testing.T) {
	for _, tc := range []struct {
		facts  map[string]string
		os     string // distro:version, comma separated
		docker string
		why    bool
	}{
		{map[string]string{"osid": "ubuntu debian", "codename": "noble", "arch": "x86_64"}, "ubuntu:noble", "ubuntu:noble", false},
		{map[string]string{"osid": "ubuntu debian", "codename": "noble", "arch": "aarch64"}, "ubuntu-ports:noble", "ubuntu:noble", false},
		{map[string]string{"osid": "debian", "codename": "trixie", "arch": "aarch64"}, "debian:trixie", "debian:trixie", false},
		{map[string]string{"osid": "raspbian debian", "codename": "bookworm"}, "raspbian:bookworm", "raspbian:bookworm", false},
		{map[string]string{"osid": "almalinux rhel centos fedora", "ver": "9.6"}, "almalinux:9,epel:9", "centos:9", false},
		{map[string]string{"osid": "rocky rhel centos fedora", "ver": "10.0"}, "rocky:10,epel:10", "centos:10", false},
		{map[string]string{"osid": "centos rhel fedora", "ver": "9"}, "centos-stream:9,epel:9", "centos:9", false},
		{map[string]string{"osid": "fedora", "ver": "44"}, "fedora:44", "fedora:44", false},
		{map[string]string{"osid": "arch"}, "archlinux:", "", false},
		{map[string]string{"osid": "rhel fedora", "ver": "9.4"}, "", "rhel:9", true},
		{map[string]string{"osid": "opensuse-leap suse"}, "", "", true},
		{map[string]string{"osid": "linuxmint ubuntu debian", "codename": "xia", "ubuntu_codename": "noble", "arch": "x86_64"}, "ubuntu:noble", "ubuntu:noble", false},
		{map[string]string{"osid": "cachyos arch"}, "archlinux:", "", false},
		{map[string]string{"osid": "ol rhel fedora", "ver": "9.4"}, "", "", true},
		{map[string]string{"os": "macOS"}, "", "", true},
	} {
		osn, docker, why := serverNeeds(tc.facts)
		var got []string
		for _, n := range osn {
			got = append(got, n.distro+":"+n.version)
		}
		gotDocker := ""
		if docker != nil {
			gotDocker = docker.distro + ":" + docker.version
		}
		if strings.Join(got, ",") != tc.os || gotDocker != tc.docker || (why != "") != tc.why {
			t.Errorf("%v: got %v, %q, %q", tc.facts, got, gotDocker, why)
		}
	}
}

// TestMirrorRewrite runs the mirrors play's regular expressions through
// Python, which is what Ansible's replace and find modules use, on real
// source files.
func TestMirrorRewrite(t *testing.T) {
	py, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 not found")
	}
	const mirror = "https://mirror.example.ir"
	vars := mirrorVars(config.Sources{APTMirror: mirror + "/"})
	files := []struct{ Family, Name, In, Want string }{
		{"Debian", "ubuntu.sources",
			"Types: deb\nURIs: http://ir.archive.ubuntu.com/ubuntu/\nSuites: noble noble-updates\n\nTypes: deb\nURIs: http://security.ubuntu.com/ubuntu/\nSuites: noble-security\n",
			"Types: deb\nURIs: " + mirror + "/ubuntu/\nSuites: noble noble-updates\n\nTypes: deb\nURIs: " + mirror + "/ubuntu/\nSuites: noble-security\n"},
		{"Debian", "sources.list",
			"deb http://deb.debian.org/debian trixie main\ndeb http://security.debian.org/debian-security trixie-security main\ndeb http://ports.ubuntu.com/ubuntu-ports noble main\n",
			"deb " + mirror + "/debian trixie main\ndeb " + mirror + "/debian-security trixie-security main\ndeb " + mirror + "/ubuntu-ports noble main\n"},
		{"Debian", "previous.list", // rewritten by an earlier run with another mirror
			"deb https://mirror.arvancloud.ir/debian trixie main\ndeb https://mirror.arvancloud.ir/debian-security trixie-security main\n",
			"deb " + mirror + "/debian trixie main\ndeb " + mirror + "/debian-security trixie-security main\n"},
		{"Debian", "docker.list", // the Docker repository must not be touched
			"deb [arch=amd64] https://repo.abrha.net/docker/debian trixie stable\n", ""},
		{"RedHat", "almalinux-baseos.repo",
			"[baseos]\nname=AlmaLinux $releasever - BaseOS\nmirrorlist=https://mirrors.almalinux.org/mirrorlist/$releasever/baseos\n# baseurl=https://repo.almalinux.org/almalinux/$releasever/BaseOS/$basearch/os/\nenabled=1\n",
			"[baseos]\nname=AlmaLinux $releasever - BaseOS\n#mirrorlist=https://mirrors.almalinux.org/mirrorlist/$releasever/baseos\nbaseurl=" + mirror + "/almalinux/$releasever/BaseOS/$basearch/os/\nenabled=1\n"},
		{"RedHat", "rocky.repo",
			"[baseos]\nmirrorlist=https://mirrors.rockylinux.org/mirrorlist?arch=$basearch&repo=BaseOS-$releasever$rltype\n#baseurl=http://dl.rockylinux.org/$contentdir/$releasever/BaseOS/$basearch/os/\n",
			"[baseos]\n#mirrorlist=https://mirrors.rockylinux.org/mirrorlist?arch=$basearch&repo=BaseOS-$releasever$rltype\nbaseurl=" + mirror + "/rocky/$releasever/BaseOS/$basearch/os/\n"},
		{"RedHat", "epel.repo",
			"[epel]\n#baseurl=https://download.example/pub/epel/$releasever${releasever_minor:+.$releasever_minor}/Everything/$basearch/\nmetalink=https://mirrors.fedoraproject.org/metalink?repo=epel-$releasever&arch=$basearch\n",
			"[epel]\nbaseurl=" + mirror + "/epel/$releasever${releasever_minor:+.$releasever_minor}/Everything/$basearch/\n#metalink=https://mirrors.fedoraproject.org/metalink?repo=epel-$releasever&arch=$basearch\n"},
		{"RedHat", "fedora.repo",
			"[fedora]\nmetalink=https://mirrors.fedoraproject.org/metalink?repo=fedora-$releasever&arch=$basearch\n#baseurl=http://download.example/pub/fedora/linux/releases/$releasever/Everything/$basearch/os/\n",
			"[fedora]\n#metalink=https://mirrors.fedoraproject.org/metalink?repo=fedora-$releasever&arch=$basearch\nbaseurl=" + mirror + "/fedora/linux/releases/$releasever/Everything/$basearch/os/\n"},
		{"RedHat", "centos.repo",
			"[baseos]\nmetalink=https://mirrors.centos.org/metalink?repo=centos-baseos-$stream&arch=$basearch&protocol=https,http\n[appstream]\nmetalink=https://mirrors.centos.org/metalink?repo=centos-appstream-$stream&arch=$basearch&protocol=https,http\n",
			"[baseos]\nbaseurl=" + mirror + "/centos-stream/$stream/BaseOS/$basearch/os/\n[appstream]\nbaseurl=" + mirror + "/centos-stream/$stream/AppStream/$basearch/os/\n"},
		{"RedHat", "centos-addons.repo", // sections without a mirror base URL keep their metalinks
			"[nfv]\nmetalink=https://mirrors.centos.org/metalink?repo=centos-nfv-$stream&arch=$basearch&protocol=https,http\nenabled=0\n[extras-common]\nmetalink=https://mirrors.centos.org/metalink?repo=centos-extras-sig-extras-common-$stream&arch=$basearch&protocol=https,http\n",
			"[nfv]\nmetalink=https://mirrors.centos.org/metalink?repo=centos-nfv-$stream&arch=$basearch&protocol=https,http\nenabled=0\n[extras-common]\nbaseurl=" + mirror + "/centos-stream/SIGs/$stream/extras/$basearch/extras-common/\n"},
	}

	dir := t.TempDir()
	in := map[string]any{"vars": vars, "files": files}
	for i := range files {
		p := filepath.Join(dir, files[i].Name)
		if err := os.WriteFile(p, []byte(files[i].In), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "metalinks.py"), metalinksScript, 0o644); err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(in)
	if err := os.WriteFile(filepath.Join(dir, "in.json"), data, 0o644); err != nil {
		t.Fatal(err)
	}
	// Mirrors the tasks in plays/mirrors-rewrite.yml, in order.
	script := `
import json, re, sys, os, subprocess
d = sys.argv[1]
cfg = json.load(open(os.path.join(d, "in.json")))
v = cfg["vars"]; m = v["interzone_os_mirror"]
for f in cfg["files"]:
    path = os.path.join(d, f["Name"])
    text = open(path).read()
    for s in v["interzone_sources"][f["Family"]]:
        if not re.search(s.get("find") or s["match"], text):
            continue
        if s["name"] == "CentOS Stream":
            for repo, p in (("baseos", "$stream/BaseOS/$basearch/os/"), ("appstream", "$stream/AppStream/$basearch/os/"),
                            ("crb", "$stream/CRB/$basearch/os/"), ("extras-sig-extras-common", "SIGs/$stream/extras/$basearch/extras-common/")):
                text = re.sub(r"^metalink=https://mirrors\.centos\.org/metalink\?repo=centos-" + repo + r"-\$stream&.*$",
                              "baseurl=https://mirror.stream.centos.org/" + p, text, flags=re.M)
        if f["Family"] == "RedHat":
            text = re.sub(r"^#\s*baseurl\s*=\s*(" + s["match"] + ")", r"baseurl=\1", text, flags=re.M)
        text = re.sub(s["match"], m + "/" + s["to"], text, flags=re.M)
    open(path, "w").write(text)
    if f["Family"] == "RedHat":
        subprocess.run([sys.executable, "-I", os.path.join(d, "metalinks.py"), m, path], check=True)
`
	out, err := exec.Command(py, "-I", "-c", script, dir).CombinedOutput()
	if err != nil {
		t.Fatalf("python: %v\n%s", err, out)
	}
	for _, f := range files {
		got, _ := os.ReadFile(filepath.Join(dir, f.Name))
		want := f.Want
		if want == "" {
			want = f.In
		}
		if string(got) != want {
			t.Errorf("%s:\ngot:\n%s\nwant:\n%s", f.Name, got, want)
		}
	}
}

func TestMirrorChecks(t *testing.T) {
	fresh := "Date: " + time.Now().UTC().Format(time.RFC1123) + "\n"
	files := map[string]string{
		"/os/ubuntu/dists/noble-security/Release":           fresh,
		"/os/debian-security/dists/trixie-security/Release": fresh,
		"/docker/debian/dists/trixie/Release":               fresh,
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if body, ok := files[r.URL.Path]; ok {
			w.Write([]byte(body))
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()

	cfg := func(blackout bool) *config.Config {
		c := config.Default()
		c.Sources.Blackout = blackout
		c.Sources.APTMirror = srv.URL + "/os"
		c.Sources.DockerPackageURL = srv.URL + "/docker"
		return c
	}
	debian := map[string]string{"osid": "debian", "codename": "trixie", "arch": "x86_64"}
	alma := map[string]string{"osid": "almalinux rhel centos fedora", "ver": "9.6", "arch": "x86_64"}
	almaWithDocker := map[string]string{"osid": "almalinux", "ver": "9.6", "arch": "x86_64", "docker": "Docker version 29"}
	for _, tc := range []struct {
		name     string
		c        *config.Config
		facts    map[string]string
		want     []Level // OS mirror, then Docker mirror if checked
		contains string  // in the first finding's detail
	}{
		{"online", cfg(false), debian, nil, ""},
		{"debian", cfg(true), debian, []Level{Pass, Pass}, "carries Debian trixie"},
		{"alma", cfg(true), alma, []Level{Warn, Fail}, "doesn't carry AlmaLinux 9"},
		{"alma docker installed", cfg(true), almaWithDocker, []Level{Warn}, "Known mirrors that do: https://mirror.hostbaran.com"},
		{"rhel", cfg(true), map[string]string{"osid": "rhel", "ver": "9.4"}, []Level{Warn, Fail}, "Red Hat's CDN"},
	} {
		got := mirrorChecks(context.Background(), tc.c, tc.facts)
		var levels []Level
		for _, f := range got {
			levels = append(levels, f.Level)
		}
		if len(levels) != len(tc.want) || (len(got) > 0 && !strings.Contains(got[0].Detail, tc.contains)) {
			t.Errorf("%s: got %+v, want levels %v and %q", tc.name, got, tc.want, tc.contains)
			continue
		}
		for i := range levels {
			if levels[i] != tc.want[i] {
				t.Errorf("%s: finding %d is level %d, want %d: %+v", tc.name, i, levels[i], tc.want[i], got[i])
			}
		}
	}
}
