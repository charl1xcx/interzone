package deploy

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"interzone/internal/config"
)

type Level int

const (
	Pass Level = iota
	Warn
	Fail
)

// Finding is the outcome of one pre-deploy check.
type Finding struct {
	Group  string // "Server" or "DNS"
	Name   string
	Level  Level
	Detail string
}

// probeScript prints key=value facts about the machine it runs on. It is
// fed to `sh -s` so nothing (including the sudo password) shows up in ps.
const probeScript = `
. /etc/os-release 2>/dev/null
echo "os=$PRETTY_NAME"
echo "osid=$ID $ID_LIKE"
echo "ver=$VERSION_ID"
echo "codename=$VERSION_CODENAME"
echo "ubuntu_codename=$UBUNTU_CODENAME"
echo "arch=$(uname -m)"
echo "uid=$(id -u)"
echo "mem=$(awk '/MemTotal/{print int($2/1024)}' /proc/meminfo 2>/dev/null)"
echo "disk=$(df -Pm / 2>/dev/null | awk 'NR==2{print $4}')"
echo "python=$(command -v python3)"
echo "docker=$(docker --version 2>/dev/null | head -n1)"
echo "traefik=$(systemctl is-active matrix-traefik 2>/dev/null)"
for p in 80 443 8448; do
  if command -v ss >/dev/null 2>&1; then
    if ss -ltn 2>/dev/null | awk 'NR>1{print $4}' | grep -qE "[:.]$p\$"; then echo "port$p=busy"; else echo "port$p=free"; fi
  fi
done
echo "ips=$( (hostname -I 2>/dev/null) || (ip -o addr show scope global 2>/dev/null | awk '{print $4}' | cut -d/ -f1 | tr '\n' ' ') )"
if [ "$(id -u)" = 0 ]; then echo "sudo=root"
elif sudo -n true 2>/dev/null; then echo "sudo=nopass"
elif [ -n "$IZ_PW" ] && printf '%s\n' "$IZ_PW" | sudo -S -p '' true 2>/dev/null; then echo "sudo=password"
else echo "sudo=fail"; fi
`

func shellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

func expandHome(p string) string {
	if strings.HasPrefix(p, "~/") {
		if h, err := os.UserHomeDir(); err == nil {
			return filepath.Join(h, p[2:])
		}
	}
	return p
}

// probe runs probeScript on the target, over SSH or locally.
func probe(ctx context.Context, c *config.Config) (map[string]string, error) {
	t := c.Target
	script := "IZ_PW=" + shellQuote(t.BecomePass) + "\n" + probeScript
	var cmd *exec.Cmd
	if t.Mode == "ssh" {
		cmd = exec.CommandContext(ctx, "ssh", append(sshArgs(t), "sh", "-s")...)
	} else {
		cmd = exec.CommandContext(ctx, "sh", "-s")
	}
	cmd.Stdin = strings.NewReader(script)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		msg := strings.TrimSpace(stderr.String())
		if i := strings.LastIndex(msg, "\n"); i >= 0 {
			msg = msg[i+1:]
		}
		if msg == "" {
			msg = err.Error()
		}
		return nil, fmt.Errorf("%s", msg)
	}
	facts := map[string]string{}
	for _, l := range strings.Split(string(out), "\n") {
		if k, v, ok := strings.Cut(l, "="); ok {
			facts[k] = strings.TrimSpace(v)
		}
	}
	return facts, nil
}

// ServerChecks logs into the target and checks it can run the playbook,
// then checks every DNS record points at it.
func ServerChecks(c *config.Config) []Finding {
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()

	var out []Finding
	add := func(name string, l Level, format string, a ...any) {
		out = append(out, Finding{Group: "Server", Name: name, Level: l, Detail: fmt.Sprintf(format, a...)})
	}

	facts, err := probe(ctx, c)
	where := "this machine"
	if c.Target.Mode == "ssh" {
		where = c.Target.User + "@" + c.Target.Host
	}
	if err != nil {
		add("Connect", Fail, "can't run commands on %s: %s", where, err)
		return append(out, dnsChecks(ctx, c, nil)...)
	}
	add("Connect", Pass, "%s", where)

	switch facts["sudo"] {
	case "root":
		add("Root access", Pass, "running as root")
	case "nopass", "password":
		add("Root access", Pass, "sudo works")
	default:
		add("Root access", Fail, "sudo needs a password: enter it on the Server step")
	}

	osid := facts["osid"]
	supported := false
	for _, id := range []string{"debian", "ubuntu", "raspbian", "fedora", "rhel", "centos", "rocky", "almalinux", "arch"} {
		if strings.Contains(osid, id) {
			supported = true
		}
	}
	if supported {
		add("Operating system", Pass, "%s", facts["os"])
	} else {
		add("Operating system", Warn, "%s isn't one the playbook supports (Debian, Ubuntu, Fedora, CentOS/Rocky/Alma, Arch)", orUnknown(facts["os"]))
	}

	switch arch := facts["arch"]; arch {
	case "x86_64", "aarch64", "arm64":
		add("CPU", Pass, "%s", arch)
	default:
		add("CPU", Warn, "%s: many container images don't exist for this architecture", orUnknown(arch))
	}

	if facts["python"] == "" {
		add("Python", Fail, "python3 is missing; Ansible needs it on the server")
	} else {
		add("Python", Pass, "%s", facts["python"])
	}

	if mem, err := strconv.Atoi(facts["mem"]); err == nil {
		switch {
		case mem < 900:
			add("Memory", Fail, "%d MB; at least 1 GB is needed, 2 GB recommended", mem)
		case mem < 1800:
			add("Memory", Warn, "%d MB; 2 GB or more is recommended", mem)
		default:
			add("Memory", Pass, "%.1f GB", float64(mem)/1024)
		}
	}
	if disk, err := strconv.Atoi(facts["disk"]); err == nil {
		switch {
		case disk < 3000:
			add("Disk space", Fail, "%.1f GB free on /; container images alone need several GB", float64(disk)/1024)
		case disk < 10000:
			add("Disk space", Warn, "%.1f GB free on /; 10 GB or more is recommended", float64(disk)/1024)
		default:
			add("Disk space", Pass, "%.0f GB free", float64(disk)/1024)
		}
	}

	_, dockerRepo, _ := serverNeeds(facts)
	if d := facts["docker"]; d != "" {
		add("Docker", Pass, "%s", strings.TrimPrefix(d, "Docker version "))
	} else if dockerRepo == nil {
		add("Docker", Pass, "not installed yet; the playbook will install it from the distro's packages")
	} else if c.Sources.Blackout && c.Sources.DockerPackageURL == "" {
		add("Docker", Warn, "not installed, and no Docker package mirror is set on the Server step")
	} else {
		add("Docker", Pass, "not installed yet; the playbook will install it")
	}

	ours := facts["traefik"] == "active"
	for _, p := range []string{"80", "443", "8448"} {
		switch st := facts["port"+p]; {
		case st == "":
		case st == "free":
			add("Port "+p, Pass, "free")
		case ours:
			add("Port "+p, Pass, "used by the existing Matrix install")
		default:
			add("Port "+p, Fail, "another program is already listening on it")
		}
	}

	out = append(out, mirrorChecks(ctx, c, facts)...)
	return append(out, dnsChecks(ctx, c, strings.Fields(facts["ips"]))...)
}

// mirrorChecks makes sure the blackout mirrors carry this server's distro
// and release.
func mirrorChecks(ctx context.Context, c *config.Config, facts map[string]string) []Finding {
	if !c.Sources.Blackout {
		return nil
	}
	osNeeds, docker, why := serverNeeds(facts)
	arch := facts["arch"]
	var out []Finding
	add := func(name string, l Level, detail string) {
		out = append(out, Finding{Group: "Server", Name: name, Level: l, Detail: detail})
	}

	switch mirror := c.Sources.APTMirror; {
	case why != "":
		add("OS package mirror", Warn, why+"; its packages download from abroad")
	case mirror == "":
		add("OS package mirror", Warn, "none set; the server's packages download from abroad")
	default:
		var have, missing []string
		var alts []config.Mirror
		for _, n := range osNeeds {
			if _, err := ProbeDistro(ctx, config.MirrorOS, mirror, n.distro, n.version, arch); err != nil {
				missing = append(missing, name(n.distro, n.version)+" ("+err.Error()+")")
				alts = append(alts, config.MirrorsFor(config.MirrorOS, n.distro)...)
			} else {
				have = append(have, name(n.distro, n.version))
			}
		}
		if len(missing) == 0 {
			add("OS package mirror", Pass, "carries "+strings.Join(have, ", "))
		} else {
			add("OS package mirror", Warn, "doesn't carry "+strings.Join(missing, ", ")+
				"; those packages download from abroad. "+alternatives(alts, mirror))
		}
	}

	if docker != nil && facts["docker"] == "" && c.Sources.DockerPackageURL != "" {
		mirror := c.Sources.DockerPackageURL
		if _, err := ProbeDistro(ctx, config.MirrorDocker, mirror, docker.distro, docker.version, arch); err != nil {
			add("Docker package mirror", Fail, "Docker isn't installed and the mirror doesn't carry "+
				name(docker.distro, docker.version)+" ("+err.Error()+"). "+
				alternatives(config.MirrorsFor(config.MirrorDocker, docker.distro), mirror))
		} else {
			add("Docker package mirror", Pass, "carries "+name(docker.distro, docker.version))
		}
	}
	return out
}

// alternatives suggests known mirrors other than current.
func alternatives(ms []config.Mirror, current string) string {
	seen := map[string]bool{strings.TrimRight(current, "/"): true}
	var urls []string
	for _, m := range ms {
		if !seen[m.URL] {
			seen[m.URL] = true
			urls = append(urls, m.URL)
		}
	}
	if len(urls) == 0 {
		return "No mirror in Iran is known to carry it."
	}
	return "Known mirrors that do: " + strings.Join(urls, ", ") + "."
}

func orUnknown(s string) string {
	if s == "" {
		return "unknown"
	}
	return s
}

// dnsChecks resolves every record the setup needs and compares it with the
// server's addresses: the SSH host's, plus whatever the server reports.
func dnsChecks(ctx context.Context, c *config.Config, serverIPs []string) []Finding {
	known := map[string]bool{}
	for _, ip := range serverIPs {
		known[ip] = true
	}
	if c.Target.Mode == "ssh" && c.Target.Host != "" {
		if net.ParseIP(c.Target.Host) != nil {
			known[c.Target.Host] = true
		} else if ips, err := net.DefaultResolver.LookupHost(ctx, c.Target.Host); err == nil {
			for _, ip := range ips {
				known[ip] = true
			}
		}
	}

	recs := c.DNSRecords()
	out := make([]Finding, len(recs))
	var wg sync.WaitGroup
	for i, rec := range recs {
		wg.Add(1)
		go func(i int, rec string) {
			defer wg.Done()
			f := Finding{Group: "DNS", Name: rec}
			lctx, cancel := context.WithTimeout(ctx, 6*time.Second)
			defer cancel()
			ips, err := net.DefaultResolver.LookupHost(lctx, rec)
			switch {
			case err != nil:
				f.Level, f.Detail = Fail, "doesn't resolve; add an A record pointing at the server"
			case len(known) == 0:
				f.Level, f.Detail = Pass, strings.Join(ips, ", ")
			default:
				match := false
				for _, ip := range ips {
					match = match || known[ip]
				}
				if match {
					f.Level, f.Detail = Pass, strings.Join(ips, ", ")
				} else {
					addrs := make([]string, 0, len(known))
					for ip := range known {
						addrs = append(addrs, ip)
					}
					sort.Strings(addrs)
					if len(addrs) > 3 {
						addrs = append(addrs[:3], fmt.Sprintf("+%d more", len(addrs)-3))
					}
					f.Level = Warn
					f.Detail = fmt.Sprintf("points to %s, not the server (%s); fine only if it's behind NAT",
						strings.Join(ips, ", "), strings.Join(addrs, ", "))
				}
			}
			out[i] = f
		}(i, rec)
	}
	wg.Wait()
	return out
}

// Worst returns the most serious level among findings.
func Worst(fs []Finding) Level {
	w := Pass
	for _, f := range fs {
		w = max(w, f.Level)
	}
	return w
}
