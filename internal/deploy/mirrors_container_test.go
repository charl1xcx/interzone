package deploy

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"interzone/internal/config"
)

// TestMirrorPlayContainers runs the mirrors play against containers of
// each supported distro and checks that the package manager then works
// through the mirror. It needs Docker, ansible-playbook with the
// community.docker collection and internet access (to install Python in
// the containers from their usual sources first), so it is skipped unless
// INTERZONE_CONTAINER_TESTS=1:
//
//	INTERZONE_CONTAINER_TESTS=1 go test ./internal/deploy -run PlayContainers -v -timeout 30m
func TestMirrorPlayContainers(t *testing.T) {
	if os.Getenv("INTERZONE_CONTAINER_TESTS") == "" {
		t.Skip("set INTERZONE_CONTAINER_TESTS=1 to run the mirrors play in containers")
	}
	for _, bin := range []string{"docker", "ansible-playbook"} {
		if _, err := exec.LookPath(bin); err != nil {
			t.Skipf("%s not found", bin)
		}
	}
	const (
		hostbaran = "https://mirror.hostbaran.com"
		abrha     = "https://repo.abrha.net"
		mobinhost = "https://mirror.mobinhost.com"
		arvan     = "https://mirror.arvancloud.ir"
		cdnDocker = "https://mirror.cdn.ir/repository/docker/linux"
		// Not in Iran; used only to exercise the Rocky and Fedora rewrites,
		// which no Iranian mirror carries.
		freedif      = "https://mirror.freedif.org"
		linuxmirrors = "https://linuxmirrors.ir"
		xtom         = "https://mirrors.xtom.de"
	)
	aptUpdate := "rm -rf /var/lib/apt/lists/* && apt-get update"
	// Lists the URLs of every enabled dnf repository.
	dnfURLs := `python3 - <<'EOF'
import configparser, glob
for f in glob.glob("/etc/yum.repos.d/*.repo"):
    c = configparser.RawConfigParser(strict=False); c.read(f)
    for s in c.sections():
        if c.get(s, "enabled", fallback="1").strip() in ("1", "true", "True"):
            for k in ("baseurl", "metalink", "mirrorlist"):
                if c.has_option(s, k): print(s, k, c.get(s, k).split()[0])
EOF
`
	for _, tc := range []struct {
		name, image, mirror, docker string
		setup                       string   // installs Python, without touching the package sources
		check                       string   // must succeed after the play
		want, absent                []string // output of check must contain / must not contain
		warn                        []string // warnings the play must print
	}{
		{name: "debian", image: "debian:trixie", mirror: hostbaran,
			setup: "apt-get update -qq && apt-get install -y -qq python3 >/dev/null",
			check: aptUpdate, want: []string{"mirror.hostbaran.com/debian trixie ", "mirror.hostbaran.com/debian-security trixie-security"},
			absent: []string{"deb.debian.org"}},
		{name: "ubuntu", image: "ubuntu:24.04", mirror: arvan,
			setup: "apt-get update -qq && apt-get install -y -qq python3 >/dev/null",
			check: aptUpdate, want: []string{"mirror.arvancloud.ir/ubuntu noble-security"},
			absent: []string{"archive.ubuntu.com", "security.ubuntu.com"}},
		{name: "alma9", image: "almalinux:9", mirror: abrha, docker: cdnDocker,
			check: dnfURLs + "dnf -y makecache >/dev/null && dnf -y install python3-docker >/dev/null && echo installed",
			want: []string{abrha + "/almalinux/$releasever/BaseOS", abrha + "/epel/9/Everything",
				cdnDocker + "/centos/$releasever/$basearch/stable", "installed"},
			// epel-cisco-openh264 keeps its metalink; dnf skips it when it's unreachable.
			absent: []string{"mirrors.almalinux.org", "repo=epel-9", "download.docker.com"}},
		{name: "alma10", image: "almalinux:10", mirror: hostbaran,
			check:  dnfURLs + "dnf -y makecache >/dev/null && echo ok",
			want:   []string{hostbaran + "/almalinux/$releasever/BaseOS", hostbaran + "/epel/$releasever", "ok"},
			absent: []string{"mirrors.almalinux.org", "repo=epel-"}},
		{name: "rocky9-no-mirror", image: "rockylinux/rockylinux:9", mirror: hostbaran,
			check: dnfURLs, want: []string{"mirrors.rockylinux.org", hostbaran + "/epel/9"},
			warn: []string{"Rocky Linux isn't on " + hostbaran}},
		{name: "rocky9", image: "rockylinux/rockylinux:9", mirror: freedif,
			check: dnfURLs + "dnf -y makecache >/dev/null && echo ok", want: []string{freedif + "/rocky/$releasever/BaseOS", "ok"},
			absent: []string{"mirrors.rockylinux.org"}, warn: []string{"EPEL 9 isn't on " + freedif}},
		{name: "centos-stream9-no-mirror", image: "quay.io/centos/centos:stream9", mirror: hostbaran,
			check: dnfURLs, want: []string{"mirrors.centos.org"},
			warn: []string{"CentOS Stream isn't on " + hostbaran}},
		{name: "centos-stream9", image: "quay.io/centos/centos:stream9", mirror: xtom,
			check: dnfURLs + "dnf -y makecache >/dev/null && echo ok",
			want: []string{xtom + "/centos-stream/$stream/BaseOS/$basearch/os/", xtom + "/centos-stream/$stream/AppStream/",
				xtom + "/centos-stream/SIGs/$stream/extras/$basearch/extras-common/", xtom + "/epel/", "ok"},
			absent: []string{"repo=centos-baseos", "repo=centos-appstream", "repo=centos-extras"}},
		{name: "fedora44", image: "fedora:44", mirror: linuxmirrors,
			setup: "dnf -y -q install python3 >/dev/null",
			check: dnfURLs + "dnf -y makecache >/dev/null && echo ok", want: []string{linuxmirrors + "/fedora/linux/updates/", "ok"},
			absent: []string{"repo=fedora-$releasever", "repo=updates-released"}},
		{name: "arch", image: "archlinux:latest", mirror: mobinhost,
			setup: "pacman -Sy --noconfirm --needed python >/dev/null",
			check: "head -n1 /etc/pacman.d/mirrorlist && pacman -Syy --noconfirm",
			want:  []string{"Server = " + mobinhost + "/archlinux/$repo/os/$arch"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ctr := "interzone-test-" + tc.name
			_ = exec.Command("docker", "rm", "-f", ctr).Run()
			if out, err := exec.Command("docker", "run", "-d", "--name", ctr, tc.image, "sleep", "3600").CombinedOutput(); err != nil {
				t.Fatalf("docker run: %v\n%s", err, out)
			}
			defer exec.Command("docker", "rm", "-f", ctr).Run()
			sh := func(script string) (string, error) {
				out, err := exec.Command("docker", "exec", ctr, "sh", "-c", script).CombinedOutput()
				return string(out), err
			}
			if tc.setup != "" {
				if out, err := sh(tc.setup); err != nil {
					t.Fatalf("setup: %v\n%s", err, out)
				}
			}

			dir := t.TempDir()
			vars, _ := json.Marshal(mirrorVars(config.Sources{APTMirror: tc.mirror, DockerPackageURL: tc.docker}))
			files := map[string][]byte{
				"interzone-mirrors.yml":          mirrorsPlay,
				"interzone-mirrors-rewrite.yml":  mirrorsRewrite,
				"interzone-mirrors-metalinks.py": metalinksScript,
				"vars.json":                      vars,
				"hosts": []byte("[matrix_servers]\n" + ctr + " ansible_connection=community.docker.docker" +
					" ansible_become=false ansible_python_interpreter=/usr/bin/python3\n"),
			}
			for n, b := range files {
				if err := os.WriteFile(filepath.Join(dir, n), b, 0o644); err != nil {
					t.Fatal(err)
				}
			}
			cmd := exec.Command("ansible-playbook", "-i", filepath.Join(dir, "hosts"),
				filepath.Join(dir, "interzone-mirrors.yml"), "-e", "@"+filepath.Join(dir, "vars.json"))
			cmd.Env = append(os.Environ(), "ANSIBLE_NOCOLOR=1", "ANSIBLE_HOST_KEY_CHECKING=0")
			var play bytes.Buffer
			cmd.Stdout, cmd.Stderr = &play, &play
			if err := cmd.Run(); err != nil {
				t.Fatalf("play: %v\n%s", err, play.String())
			}
			var warnings []string
			for _, m := range playWarning.FindAllStringSubmatch(play.String(), -1) {
				warnings = append(warnings, m[1])
			}
			for _, w := range tc.warn {
				if !strings.Contains(strings.Join(warnings, "\n"), w) {
					t.Errorf("missing warning %q; got %q", w, warnings)
				}
			}
			if len(tc.warn) == 0 && len(warnings) > 0 {
				t.Errorf("unexpected warnings: %q", warnings)
			}

			out, err := sh(tc.check)
			if err != nil {
				t.Fatalf("check: %v\n%s", err, out)
			}
			for _, w := range tc.want {
				if !strings.Contains(out, w) {
					t.Errorf("check output lacks %q:\n%s", w, out)
				}
			}
			for _, a := range tc.absent {
				if strings.Contains(out, a) {
					t.Errorf("check output still has %q:\n%s", a, out)
				}
			}

			// A second run must change nothing.
			cmd = exec.Command("ansible-playbook", "-i", filepath.Join(dir, "hosts"),
				filepath.Join(dir, "interzone-mirrors.yml"), "-e", "@"+filepath.Join(dir, "vars.json"))
			cmd.Env = append(os.Environ(), "ANSIBLE_NOCOLOR=1")
			again, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("second play: %v\n%s", err, again)
			}
			if !strings.Contains(string(again), "changed=0") {
				t.Errorf("second run changed something:\n%s", again)
			}
		})
	}
}
