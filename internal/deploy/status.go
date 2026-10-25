package deploy

import (
	"bufio"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"net"
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
	Disks      []Disk
	Containers []Container
	Certs      []Cert
	Timer      string // next scheduled backup, if any
	Taken      time.Time
}

type Disk struct {
	Mount              string
	Total, Used, Avail int64 // bytes
}

type Container struct {
	Name, State, Status string
	CPU                 string // e.g. "0.52%"
	Mem                 string // e.g. "120MiB / 3.8GiB"
	MemPerc             float64
}

type Cert struct {
	Host     string
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
        awk '/^MemTotal:/{print "memtotal="$2} /^MemAvailable:/{print "memavail="$2}' /proc/meminfo
        df -Pk / /matrix 2>/dev/null | awk 'NR>1{print "disk="$6" "$2" "$3" "$4}'
        echo "timer=$(systemctl show interzone-backup.timer -p NextElapseUSecRealtime --value 2>/dev/null)"
        docker ps -a --filter name=^matrix- --format 'ps={{json .}}'
        ids=$(docker ps -q --filter name=^matrix-)
        if [ -n "$ids" ]; then docker stats --no-stream --format 'stats={{json .}}' $ids; fi
      changed_when: false
`

// Status collects the dashboard's numbers: resources and containers from
// the server, certificates from each hostname's HTTPS endpoint.
func Status(ctx context.Context, c *config.Config) (*ServerStatus, error) {
	st := &ServerStatus{Taken: time.Now()}
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		st.Certs = CheckCerts(ctx, c)
	}()
	res, err := runPlayJSON(ctx, c, "status", statusPlay, nil)
	wg.Wait()
	if err != nil {
		return st, err
	}
	parseStatus(st, res["interzone status"].String("stdout"))
	return st, nil
}

func parseStatus(st *ServerStatus, out string) {
	stats := map[string]Container{}
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
		case "ps":
			var p struct{ Names, State, Status string }
			if json.Unmarshal([]byte(v), &p) == nil {
				st.Containers = append(st.Containers, Container{Name: p.Names, State: p.State, Status: p.Status})
			}
		case "stats":
			var s struct{ Name, CPUPerc, MemUsage, MemPerc string }
			if json.Unmarshal([]byte(v), &s) == nil {
				perc, _ := strconv.ParseFloat(strings.TrimSuffix(s.MemPerc, "%"), 64)
				stats[s.Name] = Container{CPU: s.CPUPerc, Mem: s.MemUsage, MemPerc: perc}
			}
		}
	}
	for i, ct := range st.Containers {
		if s, ok := stats[ct.Name]; ok {
			st.Containers[i].CPU, st.Containers[i].Mem, st.Containers[i].MemPerc = s.CPU, s.Mem, s.MemPerc
		}
	}
	// Problems first, then by name.
	sort.SliceStable(st.Containers, func(i, j int) bool {
		a, b := st.Containers[i].State == "running", st.Containers[j].State == "running"
		if a != b {
			return !a
		}
		return st.Containers[i].Name < st.Containers[j].Name
	})
}

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
func CheckCerts(ctx context.Context, c *config.Config) []Cert {
	if c.SSL.Mode == "none" {
		return nil
	}
	recs := c.DNSRecords()
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
