package ui

import (
	"cmp"
	"fmt"
	"net/netip"
	"slices"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"interzone/internal/deploy"
	"interzone/internal/geo"
)

// mapView is the Host screen on a machine running interzone-mirror's node:
// nothing to set up, so the swarm's map fills it, with the node's numbers,
// its peers and who came and went beside it (below it, when narrow).
func (s *hostScreen) mapView(w, h int) string {
	st := s.stats
	if st == nil || s.statsFor != targetKey(s.target()) {
		st = &deploy.HostStats{}
	}
	sc := mapScene{st: st, rates: s.rates, phase: s.phase, ripples: s.ripples, big: true}
	join := func(rows []string) string { return strings.Join(rows, "\n") }
	if w >= 84 {
		sw := clampInt(w*38/100, 34, 50)
		node := s.nodeRows(st, sw)
		return lipgloss.JoinHorizontal(lipgloss.Top, join(swarmMap(sc, w-sw-2, h)), "  ",
			join(append(node, s.peerRows(st, sw, h-len(node))...)))
	}
	mh := max(12, h*3/5)
	below := h - mh - 1
	node := s.nodeRows(st, w)
	if w < 70 {
		rows := append(node, s.peerRows(st, w, below-len(node))...)
		return join(swarmMap(sc, w, mh)) + "\n\n" + join(rows[:max(0, min(len(rows), below))])
	}
	half := (w - 2) / 2
	node = s.nodeRows(st, half)
	return join(swarmMap(sc, w, mh)) + "\n\n" + lipgloss.JoinHorizontal(lipgloss.Top,
		join(node[:min(len(node), below)]), "  ", join(s.peerRows(st, w-half-2, below+1)[1:]))
}

// nodeRows is the node's numbers, for the big map.
func (s *hostScreen) nodeRows(st *deploy.HostStats, w int) []string {
	rows := []string{sectionHeading("The node", w)}
	line := func(l string) { rows = append(rows, truncate(l, w)) }
	switch {
	case s.stats == nil:
		line(sMuted.Render("reading interzone-mirror's node…"))
	case !st.Active:
		line(sBad.Render("● stopped") + sFaint.Render("  docker start "+st.Mirror+" starts it again"))
	default:
		inbound := 0
		for _, p := range st.PeerList {
			if p.Inbound {
				inbound++
			}
		}
		line(sGood.Render("● sharing") + sMuted.Render(fmt.Sprintf(" · %s, %d came to it", plural(st.Peers, "peer"), inbound)))
		sw := clampInt(w-14, 8, 24)
		line(sAccent.Render("↓ "+sparkOf(s.in, sw)) + sMuted.Render(" "+humanBytes(int64(st.RateIn))+"/s"))
		line(sAccent.Render("↑ "+sparkOf(s.out, sw)) + sMuted.Render(" "+humanBytes(int64(st.RateOut))+"/s"))
		line(sMuted.Render(fmt.Sprintf("in all ↓ %s  ↑ %s", humanBytes(st.TotalIn), humanBytes(st.TotalOut))))
		line(sMuted.Render(fmt.Sprintf("swarm gave %s, got %s", humanBytes(st.DataSent), humanBytes(st.DataReceived))))
		line(sMuted.Render(fmt.Sprintf("disk %s used, %s free", humanBytes(st.RepoSize), humanBytes(st.DiskFree))))
		if st.Published != "" {
			line(sMuted.Render("published ") + publishDate(st.Published))
		}
		var parts []string
		for _, p := range st.PublishedParts {
			mark := sFaint.Render("…")
			if p.Pinned {
				mark = sGood.Render("✓")
			}
			parts = append(parts, mark+partNames([]string{p.Name})[0])
		}
		if parts != nil {
			line(strings.Join(parts, " "))
		}
	}
	return rows
}

// peerRows is a blank row, the busiest peers and the comings and goings,
// in h rows at most.
func (s *hostScreen) peerRows(st *deploy.HostStats, w, h int) []string {
	var rows []string
	line := func(l string) { rows = append(rows, truncate(l, w)) }

	// The busiest peers, as many as leave room for the comings and goings.
	peers := slices.Clone(st.PeerList)
	slices.SortStableFunc(peers, func(a, b deploy.Peer) int { return cmp.Compare(b.Sent+b.Recv, a.Sent+a.Recv) })
	rows = append(rows, "", sectionHeading(plural(len(peers), "peer"), w))
	col := func(v string, n int) string { return lipgloss.NewStyle().Width(n).Render(truncate(v, n)) }
	sparkW := 9 // the traffic column, if there's room for it
	if w < 44 {
		sparkW = 0
	}
	placeW := max(8, w-25-sparkW)
	line(sFaint.Render(col("place", placeW) + col("network", 10) + col("   ms", 6) + col("traffic", sparkW) + "now"))
	room := h - 3 - min(max(len(s.comings), 1), 6)
	for i, p := range peers {
		if len(rows) >= room {
			line(sFaint.Render(fmt.Sprintf("and %d more", len(peers)-i)))
			break
		}
		where, isp := peerWhere(p.Addr)
		ms := "    –"
		if p.Latency > 0 {
			ms = fmt.Sprintf("%5d", p.Latency.Milliseconds())
		}
		r := s.rates[p.ID]
		now, spark := sFaint.Render("idle"), sFaint
		if r.in+r.out > 0 {
			now, spark = sAccent.Render(humanBytes(int64(r.in+r.out))+"/s"), sGood
		}
		line(col(where, placeW) + sMuted.Render(col(isp, 10)+col(ms, 6)) + spark.Render(col(sparkOf(s.peerHist[p.ID], 8), sparkW)) + now)
	}

	rows = append(rows, "", sectionHeading("Comings and goings", w))
	if len(s.comings) == 0 {
		line(sFaint.Render("none yet; each ripples on the map"))
	}
	for i := len(s.comings) - 1; i >= 0 && len(rows) < h; i-- {
		c := s.comings[i]
		mark := sGood.Render("+ came")
		if !c.join {
			mark = sBad.Render("− left")
		}
		where, isp := peerWhere(c.peer.Addr)
		line(sFaint.Render(c.at.Format("15:04")) + " " + mark + " " + where + sFaint.Render(" "+isp))
	}
	return rows[:min(len(rows), max(h, 0))]
}

// peerWhere names where an address is, and its network if in Iran.
func peerWhere(a netip.Addr) (string, string) {
	p, ok := geo.Locate(a)
	switch {
	case !ok && (a.IsPrivate() || a.IsLoopback()):
		return "local network", ""
	case !ok:
		return "unknown", ""
	case p.Country == "IR":
		return p.Town, p.ISP
	}
	return p.Country, "abroad"
}

// sparkOf draws rates as a sparkline w wide, scaled to their highest.
func sparkOf(vals []float64, w int) string {
	top := 1.0
	for _, v := range vals {
		top = max(top, v)
	}
	scaled := make([]float64, len(vals))
	for i, v := range vals {
		scaled[i] = v / top
	}
	return lipgloss.NewStyle().Width(w).Render(sparkline(scaled, w)) // fills in as readings come
}

func publishDate(p string) string { return strings.NewReplacer("T", " ", "Z", " UTC").Replace(p) }
