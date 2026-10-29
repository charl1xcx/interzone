package deploy

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/hex"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"interzone/internal/ipfs"
)

// The IPFS source (IPFS.md in interzone-mirror): the server runs an IPFS
// daemon in interzone's private swarm and every download of the run comes
// through it. Packages and git go through its gateway, images through
// serve-registry. Where Ansible runs over SSH, an SSH forward reaches the
// gateway and the registry.

const (
	ipfsBin      = "/opt/interzone"
	ipfsRepo     = "/var/lib/interzone-ipfs"
	ipfsAPI      = "/ip4/127.0.0.1/tcp/45001"
	ipfsGateway  = "127.0.0.1:48080"
	ipfsRegistry = "127.0.0.1:45050"
	ipfsUser     = "interzone-ipfs"
)

// ipfsRun is what a run set up on the server and must undo.
type ipfsRun struct {
	tunnel *exec.Cmd
	seed   bool
}

// usesDownloads says whether a plan fetches anything the swarm provides.
func usesDownloads(plan []Step) bool {
	for _, s := range plan {
		switch s {
		case StepPlaybook, StepRoles, StepMirrors, StepInstall:
			return true
		}
	}
	return false
}

func (r *Runner) ipfsSetup(ctx context.Context) error {
	r.begin(StepIPFS)
	t := r.cfg.Target
	facts, err := probe(ctx, r.cfg)
	if err != nil {
		return fmt.Errorf("can't log in to the server: %w", err)
	}
	arch := ociArch(facts["arch"])
	if arch != "amd64" && arch != "arm64" {
		return fmt.Errorf("IPFS deploys need an amd64 or arm64 server, not %s", facts["arch"])
	}
	seed := r.cfg.Sources.IPFSSeed == "yes"
	r.ipfs = &ipfsRun{seed: seed}

	fromWeb, webErr := indexFromWeb(ctx)
	want := ipfs.KuboVersion
	if fromWeb != nil {
		want = fromWeb.Kubo
	}
	have, _ := r.output(ctx, t, ipfsBin+"/ipfs version --number 2>/dev/null || true")
	if have = strings.TrimSpace(have); "v"+have != want {
		bin, from, err := kuboBinary(ctx, arch, want, fromWeb)
		switch {
		case err == nil:
			r.note(StepIPFS, fmt.Sprintf("installing IPFS %s (from %s)", want, from))
			if err := r.upload(ctx, ipfsBin+"/ipfs", bin); err != nil {
				return err
			}
		case have != "":
			r.note(StepIPFS, "! couldn't get IPFS "+want+" ("+err.Error()+"); keeping "+have)
		default:
			return fmt.Errorf("couldn't get the IPFS daemon for the server: %w. An offline bundle carries it", err)
		}
	}

	r.note(StepIPFS, "starting the IPFS daemon in interzone's swarm"+map[bool]string{true: ", seeding for others", false: ""}[seed])
	if _, err := r.output(ctx, t, ipfsConfigScript(seed)); err != nil {
		return fmt.Errorf("starting IPFS on the server: %w", err)
	}

	idx := fromWeb
	r.note(StepIPFS, "looking up the newest publish in the swarm…")
	if fromSwarm, err := r.indexFromSwarm(ctx); err != nil {
		r.note(StepIPFS, "! the swarm didn't answer: "+err.Error())
	} else if idx == nil || fromSwarm.Version > idx.Version {
		idx = fromSwarm
	}
	if idx == nil {
		return fmt.Errorf("neither the swarm nor %s could be reached (%v)", ipfs.IndexURL, webErr)
	}
	if idx.Version < r.cfg.Sources.IPFSVersion {
		return fmt.Errorf("the newest publish found (%s) is older than one this server already used; refusing it", idx.Created)
	}
	r.note(StepIPFS, fmt.Sprintf("using the publish of %s: playbook %s, interzone %s", idx.Created, short(idx.Playbook.Commit), idx.Interzone))

	if err := r.registryBinary(ctx, arch, idx); err != nil {
		return err
	}
	if _, err := r.output(ctx, t, registryScript(idx.Parts.OCI)); err != nil {
		return fmt.Errorf("starting the image registry on the server: %w", err)
	}

	gateway := ipfsGateway
	if t.Mode == "ssh" {
		if gateway, err = r.ipfsTunnel(ctx); err != nil {
			return err
		}
	}
	saved := r.cfg.Sources
	saved.IPFSVersion = idx.Version
	onServer := "http://" + ipfsGateway + "/ipfs/"
	here := "http://" + gateway + "/ipfs/"
	s := &r.cfg.Sources
	s.Blackout = false
	s.APTMirror = onServer + idx.Parts.Packages
	s.DockerPackageURL = onServer + idx.Parts.Packages + "/docker"
	s.PyPIIndex = here + idx.Parts.Packages + "/pypi/simple"
	s.PlaybookGit = here + idx.Parts.Git + "/" + idx.Playbook.Repo + ".git"
	s.RolesGitBase = here + idx.Parts.Git + "/"
	s.UseRegistry, s.RegistryPrefix = true, ipfsRegistry
	if r.cfg.PinnedCommit == "" {
		r.cfg.PinnedCommit, r.pinWhy = idx.Playbook.Commit, "the IPFS publish"
	}
	r.env = Env(r.cfg)
	r.emit(Event{Step: StepIPFS, Sources: &saved})
	return nil
}

// ipfsDone stops what the run started; with seeding on, the daemon keeps
// running.
func (r *Runner) ipfsDone() {
	if r.ipfs == nil {
		return
	}
	if r.ipfs.tunnel != nil && r.ipfs.tunnel.Process != nil {
		_ = r.ipfs.tunnel.Process.Kill()
		_ = r.ipfs.tunnel.Wait()
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	stop := "systemctl stop interzone-registry 2>/dev/null; true"
	if !r.ipfs.seed {
		stop += "; systemctl disable --now interzone-ipfs 2>/dev/null; true"
	}
	_, _ = r.output(ctx, r.cfg.Target, stop)
}

// ipfsConfigScript installs the daemon as a service in the private swarm
// and waits until it answers. Only seeders listen for others.
func ipfsConfigScript(seed bool) string {
	listen, routing, enable := `[]`, "dhtclient", "systemctl disable interzone-ipfs 2>/dev/null || true"
	if seed {
		listen, routing, enable = `["/ip4/0.0.0.0/tcp/4001", "/ip6/::/tcp/4001"]`, "dhtserver", "systemctl enable interzone-ipfs"
	}
	q := func(s string) string { return shellQuote(s) }
	return `set -e
id -u ` + ipfsUser + ` >/dev/null 2>&1 || useradd --system --home-dir ` + ipfsRepo + ` --shell /usr/sbin/nologin ` + ipfsUser + `
mkdir -p ` + ipfsRepo + ` && chown ` + ipfsUser + `: ` + ipfsRepo + ` && chmod 700 ` + ipfsRepo + `
ipfs() { runuser -u ` + ipfsUser + ` -- env IPFS_PATH=` + ipfsRepo + ` ` + ipfsBin + `/ipfs "$@"; }
[ -f ` + ipfsRepo + `/config ] || ipfs init --empty-repo >/dev/null
printf '%s' ` + q(ipfs.SwarmKey) + ` > ` + ipfsRepo + `/swarm.key && chown ` + ipfsUser + `: ` + ipfsRepo + `/swarm.key
ipfs config --json Bootstrap ` + q(`["`+strings.Join(ipfs.Bootstrap, `","`)+`"]`) + `
ipfs config --json AutoConf.Enabled false
ipfs config --json AutoTLS.Enabled false
ipfs config --json DNS.Resolvers '{}'
ipfs config --json Routing.DelegatedRouters '[]'
ipfs config --json Ipns.DelegatedPublishers '[]'
ipfs config Routing.Type ` + routing + `
ipfs config --json Discovery.MDNS.Enabled false
ipfs config --json Addresses.Swarm ` + q(listen) + `
ipfs config Addresses.API ` + ipfsAPI + `
ipfs config Addresses.Gateway /ip4/` + strings.Replace(ipfsGateway, ":", "/tcp/", 1) + `
cat > /etc/systemd/system/interzone-ipfs.service <<'EOF'
[Unit]
Description=IPFS daemon in interzone's swarm
After=network-online.target
Wants=network-online.target

[Service]
User=` + ipfsUser + `
Environment=IPFS_PATH=` + ipfsRepo + ` LIBP2P_FORCE_PNET=1 LIBP2P_TCP_MUX=false
ExecStart=` + ipfsBin + `/ipfs daemon
Restart=on-failure
NoNewPrivileges=yes
ProtectSystem=strict
ProtectHome=yes
PrivateTmp=yes
ReadWritePaths=` + ipfsRepo + `

[Install]
WantedBy=multi-user.target
EOF
systemctl daemon-reload
systemctl restart interzone-ipfs
` + enable + `
i=0; until ipfs --api ` + ipfsAPI + ` id >/dev/null 2>&1; do i=$((i+1)); [ $i -lt 60 ] || { journalctl -u interzone-ipfs -n 5 --no-pager >&2; exit 1; }; sleep 1; done`
}

// registryScript runs serve-registry for the run, as a throwaway user.
func registryScript(oci string) string {
	return `set -e
systemctl stop interzone-registry 2>/dev/null || true
systemctl reset-failed interzone-registry 2>/dev/null || true
systemd-run --quiet --unit=interzone-registry --property=DynamicUser=yes ` + ipfsBin + `/interzone serve-registry -layout http://` + ipfsGateway + `/ipfs/` + oci + ` -listen ` + ipfsRegistry + `
i=0; until ss -ltn | grep -q ' ` + ipfsRegistry + ` '; do
  i=$((i+1)); systemctl is-active -q interzone-registry || { journalctl -u interzone-registry -n 5 --no-pager >&2; exit 1; }
  [ $i -lt 300 ] || { echo "the image list didn't arrive from the swarm within 5 minutes" >&2; exit 1; }; sleep 1
done`
}

// ipfsEntryReachable dials the swarm's entry nodes from here, a hint of
// whether the server can.
func ipfsEntryReachable(ctx context.Context) (string, error) {
	var d net.Dialer
	var errs []string
	for _, a := range ipfs.Bootstrap {
		p := strings.Split(a, "/") // /dns4/<host>/tcp/<port>/…
		ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		c, err := d.DialContext(ctx, "tcp", net.JoinHostPort(p[2], p[4]))
		cancel()
		if err == nil {
			c.Close()
			return p[2] + ":" + p[4] + " answers", nil
		}
		errs = append(errs, err.Error())
	}
	return "", fmt.Errorf("%s", strings.Join(errs, "; "))
}

// indexFromWeb reads the newest index over HTTPS.
func indexFromWeb(ctx context.Context) (*ipfs.Index, error) {
	get := func(name string) ([]byte, error) {
		ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
		defer cancel()
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, ipfs.IndexURL+name, nil)
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			return nil, err
		}
		defer res.Body.Close()
		if res.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("%s: %s", name, res.Status)
		}
		return io.ReadAll(io.LimitReader(res.Body, 1<<20))
	}
	idx, err := get("index.json")
	if err != nil {
		return nil, err
	}
	sig, err := get("index.json.sig")
	if err != nil {
		return nil, err
	}
	return ipfs.Verify(idx, sig)
}

// indexFromSwarm resolves the IPNS name on the server's daemon.
func (r *Runner) indexFromSwarm(ctx context.Context) (*ipfs.Index, error) {
	out, err := r.output(ctx, r.cfg.Target, `set -e
ipfs() { runuser -u `+ipfsUser+` -- env IPFS_PATH=`+ipfsRepo+` `+ipfsBin+`/ipfs --api `+ipfsAPI+` "$@"; }
root=$(ipfs name resolve --timeout=90s -r /ipns/`+ipfs.Name+`)
ipfs cat --timeout=60s "$root/index.json.sig" | head -c 4096
echo "#"
ipfs cat --timeout=60s "$root/index.json" | head -c 1048576`)
	if err != nil {
		return nil, err
	}
	sig, idx, ok := strings.Cut(out, "\n#\n")
	if !ok {
		return nil, fmt.Errorf("unexpected answer from the daemon")
	}
	return ipfs.Verify([]byte(idx), []byte(sig))
}

// kuboBinary finds the IPFS daemon for arch: a copy kept from before, the
// mirror's (checked against the signed index), or the IPFS project's
// (checked against its published SHA-512).
func kuboBinary(ctx context.Context, arch, version string, idx *ipfs.Index) ([]byte, string, error) {
	name := "ipfs-linux-" + arch
	cache := filepath.Join(dataDir(), "ipfs", version, name)
	if b, err := os.ReadFile(cache); err == nil {
		return b, "this computer", nil
	}
	keep := func(b []byte) {
		if os.MkdirAll(filepath.Dir(cache), 0o755) == nil {
			_ = os.WriteFile(cache, b, 0o755)
		}
	}
	var errs []string
	if idx != nil && idx.Bin[name] != "" {
		b, err := fetchURL(ctx, ipfs.IndexURL+"bin/"+name, 200<<20)
		if err == nil && sha256Hex(b) == idx.Bin[name] {
			keep(b)
			return b, strings.TrimSuffix(ipfs.IndexURL, "/"), nil
		}
		errs = append(errs, fmt.Sprintf("mirror: %v", errOr(err, "checksum mismatch")))
	}
	file := fmt.Sprintf("kubo_%s_linux-%s.tar.gz", version, arch)
	for _, base := range []string{"https://github.com/ipfs/kubo/releases/download/" + version + "/", "https://dist.ipfs.tech/kubo/" + version + "/"} {
		host := strings.Split(base, "/")[2]
		b, err := func() ([]byte, error) {
			tgz, err := fetchURL(ctx, base+file, 200<<20)
			if err != nil {
				return nil, err
			}
			sum, err := fetchURL(ctx, base+file+".sha512", 1024)
			if err != nil || len(bytes.Fields(sum)) == 0 {
				return nil, fmt.Errorf("no checksum: %v", err)
			}
			return untarIPFS(tgz, string(bytes.Fields(sum)[0]))
		}()
		if err == nil {
			keep(b)
			return b, host, nil
		}
		errs = append(errs, host+": "+err.Error())
	}
	return nil, "", fmt.Errorf("%s", strings.Join(errs, "; "))
}

func errOr(err error, s string) string {
	if err != nil {
		return err.Error()
	}
	return s
}

func sha256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func fetchURL(ctx context.Context, url string, limit int64) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s", res.Status)
	}
	return io.ReadAll(io.LimitReader(res.Body, limit))
}

// registryBinary puts an interzone for the server's arch at
// /opt/interzone/interzone: this one if it fits, else the release in the
// swarm, checked against the signed index.
func (r *Runner) registryBinary(ctx context.Context, arch string, idx *ipfs.Index) error {
	t := r.cfg.Target
	cur, _ := r.output(ctx, t, "sha256sum "+ipfsBin+"/interzone 2>/dev/null || true")
	if runtime.GOOS == "linux" && runtime.GOARCH == arch {
		self, err := os.Executable()
		if err != nil {
			return err
		}
		b, err := os.ReadFile(self)
		if err != nil {
			return err
		}
		if strings.HasPrefix(cur, sha256Hex(b)) {
			return nil
		}
		r.note(StepIPFS, "copying interzone to the server for its image registry")
		return r.upload(ctx, ipfsBin+"/interzone", b)
	}
	name := "interzone-linux-" + arch
	want := idx.Bin[name]
	if want == "" {
		return fmt.Errorf("the publish has no %s", name)
	}
	if strings.HasPrefix(cur, want) {
		return nil
	}
	r.note(StepIPFS, "fetching interzone "+idx.Interzone+" for the server's image registry from the swarm")
	_, err := r.output(ctx, t, `set -e
runuser -u `+ipfsUser+` -- env IPFS_PATH=`+ipfsRepo+` `+ipfsBin+`/ipfs --api `+ipfsAPI+` cat --timeout=10m /ipfs/`+idx.Parts.Bin+`/`+name+` > `+ipfsBin+`/interzone.new
echo "`+want+`  `+ipfsBin+`/interzone.new" | sha256sum -c --quiet
chmod 755 `+ipfsBin+`/interzone.new && mv `+ipfsBin+`/interzone.new `+ipfsBin+`/interzone`)
	return err
}

// upload writes b to path on the server, as root, mode 0755.
func (r *Runner) upload(ctx context.Context, path string, b []byte) error {
	t := r.cfg.Target
	dir := filepath.Dir(path)
	cmd := targetCmd(ctx, t, asRoot("sh -c "+shellQuote("mkdir -p "+dir+" && cat > "+path+".new && chmod 755 "+path+".new && mv "+path+".new "+path)))
	cmd.Stdin = io.MultiReader(strings.NewReader(t.BecomePass+"\n"), bytes.NewReader(b))
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("copying %s to the server: %s", filepath.Base(path), lastLine(stderr.String(), err))
	}
	return nil
}

// ipfsTunnel forwards a local port to the server's gateway, and the
// registry's own port, which the image check reaches by the same name the
// server pulls by.
func (r *Runner) ipfsTunnel(ctx context.Context) (string, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", err
	}
	local := l.Addr().String()
	l.Close()
	forwards := []string{"-L", local + ":" + ipfsGateway}
	if l, err := net.Listen("tcp", ipfsRegistry); err == nil {
		l.Close()
		forwards = append(forwards, "-L", ipfsRegistry+":"+ipfsRegistry)
	} else {
		r.note(StepIPFS, "! "+ipfsRegistry+" is taken on this computer, so the image check can't reach the server's registry")
	}
	args := append([]string{"-N", "-o", "ExitOnForwardFailure=yes"}, forwards...)
	cmd := exec.Command("ssh", append(args, sshArgs(r.cfg.Target)...)...)
	if err := cmd.Start(); err != nil {
		return "", err
	}
	r.ipfs.tunnel = cmd
	for i := 0; i < 100; i++ {
		if c, err := net.DialTimeout("tcp", local, time.Second); err == nil {
			c.Close()
			return local, nil
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(200 * time.Millisecond):
		}
	}
	return "", fmt.Errorf("the SSH forward to the server's IPFS gateway didn't come up")
}

// untarIPFS takes the daemon out of kubo's release tarball after checking
// the tarball against its SHA-512.
func untarIPFS(tgz []byte, sha512hex string) ([]byte, error) {
	if sum := sha512.Sum512(tgz); hex.EncodeToString(sum[:]) != sha512hex {
		return nil, fmt.Errorf("checksum mismatch")
	}
	zr, err := gzip.NewReader(bytes.NewReader(tgz))
	if err != nil {
		return nil, err
	}
	tr := tar.NewReader(zr)
	for {
		h, err := tr.Next()
		if err != nil {
			return nil, fmt.Errorf("no kubo/ipfs in the tarball")
		}
		if h.Name == "kubo/ipfs" {
			return io.ReadAll(io.LimitReader(tr, 200<<20))
		}
	}
}
