package deploy

import (
	"bufio"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"net"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"interzone/internal/config"
)

// ServerStatus is a snapshot of the server for the dashboard.
type ServerStatus struct {
	Load       [3]float64
	CPUs       int
	Uptime     time.Duration
	MemTotal   int64 // bytes
	MemAvail   int64
	SwapTotal  int64
	SwapFree   int64
	OS         string // e.g. "Debian GNU/Linux 12 (bookworm)"
	Kernel     string
	Docker     string // the Docker engine's version
	Hostname   string
	Disks      []Disk
	Containers []Container
	Certs      []Cert
	Hosts      []string            // what Traefik serves for Matrix
	Served     map[string][]string // host: the containers serving it
	Timer      string              // next scheduled backup, if any
	Taken      time.Time
}

type Disk struct {
	Mount              string
	Total, Used, Avail int64 // bytes
}

type Container struct {
	Name, State, Status string
	Image               string
	CPU                 string // e.g. "0.52%"
	Mem                 string // e.g. "120MiB / 3.8GiB"
	MemPerc             float64
	Net                 string // received / sent, e.g. "1.2MB / 800kB"
	Restarts            int
	Health              string // healthy, unhealthy, starting, or "" without a health check
}

// CPUPerc is CPU as a number, in percent of one core.
func (c Container) CPUPerc() float64 {
	f, _ := strconv.ParseFloat(strings.TrimSuffix(c.CPU, "%"), 64)
	return f
}

type Cert struct {
	Host     string
	Services string // what's served there, e.g. "Element Web, Cinny"
	NotAfter time.Time
	Issuer   string
	Err      string
}

const statusPlay = `- name: interzone status
  hosts: matrix_servers
  become: true
  gather_facts: false
  tasks:
    - name: interzone status
      ansible.builtin.shell: !unsafe |
        echo "load=$(cut -d' ' -f1-3 /proc/loadavg)"
        echo "cpus=$(nproc)"
        echo "uptime=$(cut -d' ' -f1 /proc/uptime)"
        awk '/^MemTotal:/{print "memtotal="$2} /^MemAvailable:/{print "memavail="$2} /^SwapTotal:/{print "swaptotal="$2} /^SwapFree:/{print "swapfree="$2}' /proc/meminfo
        (. /etc/os-release 2>/dev/null && echo "os=$PRETTY_NAME")
        echo "kernel=$(uname -r)"
        echo "hostname=$(hostname)"
        echo "docker=$(docker version --format '{{.Server.Version}}' 2>/dev/null)"
        df -Pk / /matrix 2>/dev/null | awk 'NR>1{print "disk="$6" "$2" "$3" "$4}'
        echo "timer=$(systemctl show interzone-backup.timer -p NextElapseUSecRealtime --value 2>/dev/null)"
        docker ps -a --filter name=^matrix- --format 'ps={{json .}}'
        ids=$(docker ps -q --filter name=^matrix-)
        if [ -n "$ids" ]; then docker stats --no-stream --format 'stats={{json .}}' $ids; fi
        all=$(docker ps -aq --filter name=^matrix-)
        if [ -n "$all" ]; then docker inspect --format 'inspect={{.Name}} {{.RestartCount}} {{if .State.Health}}{{.State.Health.Status}}{{end}}' $all; fi
        docker ps --filter name=^matrix- --filter label=traefik.enable=true --format 'labels={{.Names}} {{.Labels}}'
      changed_when: false
`

// Status collects the dashboard's numbers: resources and containers from
// the server, certificates from each hostname's HTTPS endpoint. The
// hostnames are the ones the playbook gave Traefik, read from the running
// containers, so components moved to other domains are checked where they
// are; before anything runs behind Traefik, the usual ones are guessed.
func Status(ctx context.Context, c *config.Config) (*ServerStatus, error) {
	st := &ServerStatus{Taken: time.Now()}
	res, err := runPlayJSON(ctx, c, "status", statusPlay, nil)
	if err == nil {
		parseStatus(st, res["interzone status"].String("stdout"))
	}
	hosts := st.Hosts
	if len(hosts) == 0 {
		hosts = c.DNSRecords()
	}
	st.Certs = CheckCerts(ctx, c, hosts)
	known := c.HostServices()
	for i, ct := range st.Certs {
		st.Certs[i].Services = known[ct.Host]
		if st.Certs[i].Services == "" {
			st.Certs[i].Services = strings.Join(st.Served[ct.Host], ", ")
		}
	}
	return st, err
}

var hostRule = regexp.MustCompile("Host\\(`([^`]+)`\\)")

// served notes the hostnames in a container's Traefik rules, from a
// "name labels" line.
func (st *ServerStatus) served(line string) {
	name, labels, _ := strings.Cut(line, " ")
	name = strings.TrimPrefix(name, "matrix-")
	if st.Served == nil {
		st.Served = map[string][]string{}
	}
	for _, m := range hostRule.FindAllStringSubmatch(labels, -1) {
		if !slices.Contains(st.Hosts, m[1]) {
			st.Hosts = append(st.Hosts, m[1])
		}
		if !slices.Contains(st.Served[m[1]], name) {
			st.Served[m[1]] = append(st.Served[m[1]], name)
		}
	}
}

func parseStatus(st *ServerStatus, out string) {
	stats, inspect := map[string]Container{}, map[string]Container{}
	sc := bufio.NewScanner(strings.NewReader(out))
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	for sc.Scan() {
		k, v, ok := strings.Cut(sc.Text(), "=")
		if !ok {
			continue
		}
		f := strings.Fields(v)
		switch k {
		case "load":
			for i := 0; i < 3 && i < len(f); i++ {
				st.Load[i], _ = strconv.ParseFloat(f[i], 64)
			}
		case "cpus":
			st.CPUs, _ = strconv.Atoi(v)
		case "uptime":
			s, _ := strconv.ParseFloat(v, 64)
			st.Uptime = time.Duration(s) * time.Second
		case "memtotal":
			n, _ := strconv.ParseInt(v, 10, 64)
			st.MemTotal = n * 1024
		case "memavail":
			n, _ := strconv.ParseInt(v, 10, 64)
			st.MemAvail = n * 1024
		case "swaptotal":
			n, _ := strconv.ParseInt(v, 10, 64)
			st.SwapTotal = n * 1024
		case "swapfree":
			n, _ := strconv.ParseInt(v, 10, 64)
			st.SwapFree = n * 1024
		case "os":
			st.OS = strings.TrimSpace(v)
		case "kernel":
			st.Kernel = strings.TrimSpace(v)
		case "hostname":
			st.Hostname = strings.TrimSpace(v)
		case "docker":
			st.Docker = strings.TrimSpace(v)
		case "inspect":
			if len(f) >= 2 {
				n, _ := strconv.Atoi(f[1])
				h := ""
				if len(f) > 2 {
					h = f[2]
				}
				inspect[strings.TrimPrefix(f[0], "/")] = Container{Restarts: n, Health: h}
			}
		case "disk":
			if len(f) == 4 {
				d := Disk{Mount: f[0]}
				d.Total, _ = strconv.ParseInt(f[1], 10, 64)
				d.Used, _ = strconv.ParseInt(f[2], 10, 64)
				d.Avail, _ = strconv.ParseInt(f[3], 10, 64)
				d.Total, d.Used, d.Avail = d.Total*1024, d.Used*1024, d.Avail*1024
				dup := false
				for _, o := range st.Disks {
					dup = dup || o.Total == d.Total && o.Used == d.Used // /matrix on the root filesystem
				}
				if !dup {
					st.Disks = append(st.Disks, d)
				}
			}
		case "timer":
			st.Timer = strings.TrimSpace(v)
		case "labels":
			st.served(v)
		case "ps":
			var p struct{ Names, State, Status, Image string }
			if json.Unmarshal([]byte(v), &p) == nil {
				st.Containers = append(st.Containers, Container{Name: p.Names, State: p.State, Status: p.Status, Image: p.Image})
			}
		case "stats":
			var s struct{ Name, CPUPerc, MemUsage, MemPerc, NetIO string }
			if json.Unmarshal([]byte(v), &s) == nil {
				perc, _ := strconv.ParseFloat(strings.TrimSuffix(s.MemPerc, "%"), 64)
				stats[s.Name] = Container{CPU: s.CPUPerc, Mem: s.MemUsage, MemPerc: perc, Net: s.NetIO}
			}
		}
	}
	sort.Strings(st.Hosts)
	for i, ct := range st.Containers {
		if s, ok := stats[ct.Name]; ok {
			st.Containers[i].CPU, st.Containers[i].Mem, st.Containers[i].MemPerc, st.Containers[i].Net = s.CPU, s.Mem, s.MemPerc, s.Net
		}
		if in, ok := inspect[ct.Name]; ok {
			st.Containers[i].Restarts, st.Containers[i].Health = in.Restarts, in.Health
		}
	}
	// Problems first, then by name.
	sort.SliceStable(st.Containers, func(i, j int) bool {
		a, b := st.Containers[i].Healthy(), st.Containers[j].Healthy()
		if a != b {
			return !a
		}
		return st.Containers[i].Name < st.Containers[j].Name
	})
}

// Healthy reports whether a container runs and passes its health check.
func (c Container) Healthy() bool { return c.State == "running" && c.Health != "unhealthy" }

// serverAddr is where the server's HTTPS port is reached from here: its
// SSH address, or this machine.
func serverAddr(c *config.Config) string {
	if c.Target.Mode == "ssh" && c.Target.Host != "" {
		return c.Target.Host
	}
	return "127.0.0.1"
}

// CheckCerts reads the certificate the server presents for each hostname.
// It connects to the server directly, so it works before DNS is set up.
func CheckCerts(ctx context.Context, c *config.Config, recs []string) []Cert {
	if c.SSL.Mode == "none" {
		return nil
	}
	out := make([]Cert, len(recs))
	var wg sync.WaitGroup
	for i, host := range recs {
		wg.Add(1)
		go func(i int, host string) {
			defer wg.Done()
			out[i] = Cert{Host: host}
			d := &tls.Dialer{NetDialer: &net.Dialer{Timeout: 8 * time.Second},
				Config: &tls.Config{ServerName: host, InsecureSkipVerify: true}} // only reading the dates
			cctx, cancel := context.WithTimeout(ctx, 10*time.Second)
			defer cancel()
			conn, err := d.DialContext(cctx, "tcp", net.JoinHostPort(serverAddr(c), "443"))
			if err != nil {
				out[i].Err = shortNetErr(err)
				return
			}
			defer conn.Close()
			certs := conn.(*tls.Conn).ConnectionState().PeerCertificates
			if len(certs) == 0 {
				out[i].Err = "no certificate"
				return
			}
			leaf := certs[0]
			out[i].NotAfter = leaf.NotAfter
			out[i].Issuer = leaf.Issuer.CommonName
			if out[i].Issuer == "" && len(leaf.Issuer.Organization) > 0 {
				out[i].Issuer = leaf.Issuer.Organization[0]
			}
			if leaf.VerifyHostname(host) != nil {
				out[i].Err = "the certificate isn't for " + host
			}
		}(i, host)
	}
	wg.Wait()
	return out
}

// DaysLeft is how many days a certificate is still valid.
func (c Cert) DaysLeft() int { return int(time.Until(c.NotAfter).Hours() / 24) }

func (c Cert) String() string {
	if c.Err != "" {
		return c.Err
	}
	return fmt.Sprintf("expires %s (%d days), %s", c.NotAfter.Local().Format("2006-01-02"), c.DaysLeft(), c.Issuer)
}
