package deploy

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"interzone/internal/config"
	"interzone/internal/ipfs"
)

// HostStats is what a hosting node (ActionHost) is doing, read from the
// node itself.
type HostStats struct {
	Installed bool // interzone-ipfs exists on the target
	Active    bool // the daemon runs
	Following bool // ipfs-follow is fetching a publish right now
	Failed    bool // its last run failed
	NextCheck time.Time
	Parts     []string // what it hosts, from PartNames
	Last      string   // ipfs-follow's last log line
	Mirror    string   // the container, when the node is interzone-mirror's entry node

	Peers            int
	RepoSize         int64
	DiskFree         int64
	RateIn, RateOut  float64 // bytes a second
	TotalIn          int64
	TotalOut         int64
	BlocksSent       int64
	DataSent         int64
	BlocksReceived   int64
	DataReceived     int64
	Published        string // when the newest publish the node knows was made
	PublishedParts   []HostPart
	Wanted, Received int64 // of the parts it hosts: their size, and how much is here
}

// HostPart is one part of the newest publish on the node.
type HostPart struct {
	Name   string
	Size   int64 // its whole size
	Pinned bool  // all of it is here and kept
}

// ReadHostStats asks the target's IPFS node for its numbers.
func ReadHostStats(ctx context.Context, t config.Target) (*HostStats, error) {
	// interzone-mirror's node is read through docker, which needs no sudo;
	// nothing hosted needs none either.
	cmd := targetCmd(ctx, t, mirrorStatsScript+`
[ -f /etc/systemd/system/interzone-ipfs.service ] || { echo installed no; exit 0; }
`+asRoot("sh -c "+shellQuote(hostStatsScript)))
	cmd.Stdin = strings.NewReader(t.BecomePass + "\n")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("%s", lastLine(stderr.String(), err))
	}
	return parseHostStats(string(out)), nil
}

// mirrorStatsScript finds interzone-mirror's compose service ipfs (see its
// IPFS.md), which publishes every part, and reads it like a hosting node.
const mirrorStatsScript = `c=$(docker ps -a --filter label=com.docker.compose.service=ipfs --format '{{.Names}} {{.Image}}' 2>/dev/null | awk '$2 ~ /kubo/ {print $1; exit}')
if [ -n "$c" ]; then
echo installed yes
echo mirror $c
echo active $(docker inspect -f '{{if .State.Running}}active{{end}}' $c)
echo parts all
echo free $(df -B1 --output=avail "$(docker inspect -f '{{range .Mounts}}{{if eq .Destination "/data/ipfs"}}{{.Source}}{{end}}{{end}}' $c)" | tail -1)
docker inspect -f '{{.State.Running}}' $c | grep -q true || exit 0
ipfs() { docker exec $c ipfs --timeout=10s "$@"; }
` + ipfsStatsScript + `
exit 0
fi`

const hostStatsScript = `echo installed yes
echo active $(systemctl is-active interzone-ipfs)
echo follow $(systemctl is-active interzone-ipfs-follow.service)
echo result $(systemctl show interzone-ipfs-follow.service -p Result --value)
n=$(systemctl list-timers interzone-ipfs-follow.timer --no-pager --no-legend | awk '{print $2, $3, $4}')
[ -n "$n" ] && echo next $(date -d "$n" +%s 2>/dev/null)
echo parts $(grep -o -- '-parts [a-z,]*' /etc/systemd/system/interzone-ipfs-follow.service | cut -d' ' -f2)
echo last $(journalctl -u interzone-ipfs-follow -n 1 -o cat --no-pager 2>/dev/null | tail -c 300)
echo free $(df -B1 --output=avail ` + ipfsRepo + ` | tail -1)
systemctl is-active -q interzone-ipfs || exit 0
ipfs() { runuser -u ` + ipfsUser + ` -- env IPFS_PATH=` + ipfsRepo + ` ` + ipfsBin + `/ipfs --api ` + ipfsAPI + ` --timeout=10s "$@"; }
` + ipfsStatsScript

// ipfsStatsScript reads a running node through the shell function ipfs.
const ipfsStatsScript = `echo peers $(ipfs swarm peers | wc -l)
echo repo $(ipfs repo stat --size-only --enc=json)
echo bw $(ipfs stats bw)
echo bitswap "{$(ipfs stats bitswap --enc=json | grep -oE '"(BlocksSent|DataSent|BlocksReceived|DataReceived)":[0-9]+' | paste -sd,)}"
# The newest publish: ipfs-follow pins its root directly, the mirror's
# publish recursively.
root=$(for r in $(ipfs pin ls --type=direct --quiet; ipfs pin ls --type=recursive --quiet); do
  echo "$(ipfs cat --offline "$r/index.json" 2>/dev/null | grep -o '"version": *[0-9]*' | grep -o '[0-9]*$') $r"
done | sort -n | tail -1 | cut -d' ' -f2)
[ -n "$root" ] || exit 0
echo root $root
echo index $(ipfs cat --offline "$root/index.json" | head -c 65536)
echo pins $(ipfs pin ls --type=recursive --quiet)
echo links $(ipfs dag get "$root" | head -c 65536)`

// siUnits are the units kubo writes sizes in (go-humanize's).
var siUnits = map[string]float64{"B": 1, "kB": 1e3, "MB": 1e6, "GB": 1e9, "TB": 1e12, "PB": 1e15}

func parseHostStats(out string) *HostStats {
	s := &HostStats{}
	var root string
	var pinned []string // both printed before links
	sc := bufio.NewScanner(strings.NewReader(out))
	sc.Buffer(make([]byte, 128<<10), 128<<10)
	num := func(v string) int64 { n, _ := strconv.ParseInt(strings.TrimSpace(v), 10, 64); return n }
	for sc.Scan() {
		k, v, _ := strings.Cut(sc.Text(), " ")
		switch k {
		case "installed":
			s.Installed = v == "yes"
		case "active":
			s.Active = v == "active"
		case "follow":
			s.Following = v == "activating"
		case "result":
			s.Failed = v != "" && v != "success"
		case "next":
			if n := num(v); n > 0 {
				s.NextCheck = time.Unix(n, 0)
			}
		case "parts":
			s.Parts, _ = ipfs.Parts(v)
		case "last":
			s.Last = strings.TrimSpace(v)
		case "free":
			s.DiskFree = num(v)
		case "peers":
			s.Peers = int(num(v))
		case "repo":
			var x struct{ RepoSize int64 }
			_ = json.Unmarshal([]byte(v), &x)
			s.RepoSize = x.RepoSize
		case "bw":
			// Only as text, rounded ("TotalIn: 603 MB ... RateIn: 12 B/s"): kubo ignores --enc here.
			f := strings.Fields(v)
			for i := 0; i+2 < len(f); i++ {
				n, err := strconv.ParseFloat(f[i+1], 64)
				mult, ok := siUnits[strings.TrimSuffix(f[i+2], "/s")]
				if err != nil || !ok {
					continue
				}
				switch f[i] {
				case "TotalIn:":
					s.TotalIn = int64(n * mult)
				case "TotalOut:":
					s.TotalOut = int64(n * mult)
				case "RateIn:":
					s.RateIn = n * mult
				case "RateOut:":
					s.RateOut = n * mult
				}
			}
		case "bitswap":
			var x struct{ BlocksSent, DataSent, BlocksReceived, DataReceived int64 }
			_ = json.Unmarshal([]byte(v), &x)
			s.BlocksSent, s.DataSent, s.BlocksReceived, s.DataReceived = x.BlocksSent, x.DataSent, x.BlocksReceived, x.DataReceived
		case "index": // only shown, so it needn't be verified
			var x struct{ Created string }
			_ = json.Unmarshal([]byte(v), &x)
			s.Published = x.Created
		case "mirror":
			s.Mirror = v
		case "root":
			root = v
		case "pins":
			pinned = strings.Fields(v)
		case "links": // the root's own block, so sizes are known before the parts arrive
			var x struct {
				Links []struct {
					Hash struct {
						Link string `json:"/"`
					}
					Name  string
					Tsize int64
				}
			}
			_ = json.Unmarshal([]byte(v), &x)
			for _, l := range x.Links {
				switch l.Name {
				case "packages", "oci", "git", "bin":
					s.PublishedParts = append(s.PublishedParts, HostPart{Name: l.Name, Size: l.Tsize, Pinned: slices.Contains(pinned, l.Hash.Link) || slices.Contains(pinned, root)})
				}
			}
		}
	}
	for _, p := range s.PublishedParts {
		if slices.Contains(s.Parts, p.Name) {
			s.Wanted += p.Size
			if p.Pinned {
				s.Received += p.Size
			}
		}
	}
	// What's still arriving is in the repo but not yet pinned.
	if s.Wanted > 0 && s.Received < s.Wanted {
		s.Received = min(s.Wanted, max(s.Received, s.RepoSize))
	}
	return s
}
