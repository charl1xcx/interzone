package ui

import (
	"fmt"
	"math"
	"sort"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"interzone/internal/deploy"
	"interzone/internal/geo"
)

// ── The Host screen's swarm map ────────────────────────────────────
//
// Iran drawn in braille dots (2×4 to a cell), the hosting node and every
// peer it's connected to placed by their IPs (geo), a line to each, and
// pulses running along them as bitswap moves data: in toward the node,
// out toward the peer. Peers abroad sit on the map's edge, toward their
// country. The node only knows its own connections; the faint dotted lines
// between peers are a guess (a small private swarm tends to connect every
// node to every other), and the legend says so.

// The part of the world drawn, in degrees, and where it's centred.
const (
	mapLonMin, mapLonMax = 43.0, 64.5
	mapLatMin, mapLatMax = 24.6, 40.1
	iranLon, iranLat     = 53.7, 32.4
)

var mapKx = math.Cos(32.4 * math.Pi / 180) // a degree of longitude is shorter here

// Layers: a cell takes the colour of the highest one drawn into it.
const (
	layerFill = iota + 1
	layerNeighbour
	layerBorder
	layerGuess
	layerLink
	layerLive
	layerPulseIn
	layerPulseOut
	layerText
)

// peerRate is how fast bitswap moves data with a peer, bytes a second.
type peerRate struct{ in, out float64 }

type mapCell struct {
	bits  uint8
	layer int
	style int    // index into the canvas's palette
	text  string // a marker or a label's letter, drawn instead of the dots
}

type mapCanvas struct {
	w, h    int // cells
	cells   []mapCell
	palette []lipgloss.Style
	scale   float64 // dots a degree (of latitude)
	ox, oy  float64 // dot offset of mapLonMin, mapLatMax
}

func newMapCanvas(w, h int) *mapCanvas {
	c := &mapCanvas{w: w, h: h, cells: make([]mapCell, w*h)}
	gw, gh := (mapLonMax-mapLonMin)*mapKx, mapLatMax-mapLatMin
	c.scale = min(float64(2*w)/gw, float64(4*h)/gh)
	c.ox = (float64(2*w) - gw*c.scale) / 2
	c.oy = (float64(4*h) - gh*c.scale) / 2
	return c
}

func (c *mapCanvas) style(st lipgloss.Style) int {
	c.palette = append(c.palette, st)
	return len(c.palette) - 1
}

// project turns lon/lat into dot coordinates.
func (c *mapCanvas) project(lon, lat float64) (float64, float64) {
	return c.ox + (lon-mapLonMin)*mapKx*c.scale, c.oy + (mapLatMax-lat)*c.scale
}

var brailleBit = [4][2]uint8{{0x01, 0x08}, {0x02, 0x10}, {0x04, 0x20}, {0x40, 0x80}}

func (c *mapCanvas) dot(x, y, layer, style int) {
	if x < 0 || y < 0 || x >= 2*c.w || y >= 4*c.h {
		return
	}
	cell := &c.cells[(y/4)*c.w+x/2]
	cell.bits |= brailleBit[y%4][x%2]
	if layer >= cell.layer {
		cell.layer, cell.style = layer, style
	}
}

// line draws from one dot to another, every step'th dot of it.
func (c *mapCanvas) line(x0, y0, x1, y1 float64, step, layer, style int) {
	n := int(math.Max(math.Abs(x1-x0), math.Abs(y1-y0)))
	for i := 0; i <= n; i += step {
		t := 0.0
		if n > 0 {
			t = float64(i) / float64(n)
		}
		c.dot(int(math.Round(x0+(x1-x0)*t)), int(math.Round(y0+(y1-y0)*t)), layer, style)
	}
}

// put writes text from a cell rightwards, over anything below layer.
func (c *mapCanvas) put(cx, cy int, s string, layer, style int) bool {
	r := []rune(s)
	cx = clampInt(cx, 0, c.w-len(r))
	if cy < 0 || cy >= c.h || cx < 0 {
		return false
	}
	for i := range r {
		if cell := c.cells[cy*c.w+cx+i]; cell.text != "" || cell.layer > layer {
			return false
		}
	}
	for i, ch := range r {
		c.cells[cy*c.w+cx+i] = mapCell{layer: layerText, style: style, text: string(ch)}
	}
	return true
}

func (c *mapCanvas) render() []string {
	rows := make([]string, c.h)
	for y := range c.h {
		var b strings.Builder
		run, runStyle := "", -1
		flush := func() {
			if run == "" {
				return
			}
			if runStyle < 0 {
				b.WriteString(run)
			} else {
				b.WriteString(c.palette[runStyle].Render(run))
			}
			run = ""
		}
		for x := range c.w {
			cell := c.cells[y*c.w+x]
			ch, st := " ", -1
			switch {
			case cell.text != "":
				ch, st = cell.text, cell.style
			case cell.bits != 0:
				ch, st = string(rune(0x2800+int(cell.bits))), cell.style
			}
			if st != runStyle {
				flush()
				runStyle = st
			}
			run += ch
		}
		flush()
		rows[y] = b.String()
	}
	return rows
}

// inside says whether a point is inside a ring (even-odd).
func inside(x, y float64, ring [][2]float64) bool {
	in := false
	for i, j := 0, len(ring)-1; i < len(ring); j, i = i, i+1 {
		xi, yi, xj, yj := ring[i][0], ring[i][1], ring[j][0], ring[j][1]
		if (yi > y) != (yj > y) && x < (xj-xi)*(y-yi)/(yj-yi)+xi {
			in = !in
		}
	}
	return in
}

// mapNode is a spot on the map: the hosting node, or the peers in one town
// or one country abroad.
type mapNode struct {
	x, y   float64 // dots
	label  string
	abroad bool
	edge   string // the arrow an abroad marker points with
	peers  int
	rate   peerRate
}

// edgePoint is where the line from Iran's middle toward x, y leaves the
// map, a little inside it, and which way it points.
func (c *mapCanvas) edgePoint(x, y float64) (float64, float64, string) {
	cx, cy := c.project(iranLon, iranLat)
	dx, dy := x-cx, y-cy
	if dx == 0 && dy == 0 {
		dx = 1
	}
	maxX, maxY := float64(2*c.w-3), float64(4*c.h-5)
	t := math.Inf(1)
	for _, s := range []float64{(2 - cx) / dx, (maxX - cx) / dx, (4 - cy) / dy, (maxY - cy) / dy} {
		if s > 0 && s < t {
			t = s
		}
	}
	ex, ey := cx+dx*t, cy+dy*t
	arrow := "▸"
	switch {
	case math.Abs(ex-2) < 1:
		arrow = "◂"
	case math.Abs(ey-4) < 1:
		arrow = "▴"
	case math.Abs(ey-maxY) < 1:
		arrow = "▾"
	}
	return ex, ey, arrow
}

// swarmMap draws the node and its peers in w×h cells (h rows of map, then
// two of legend), phase seconds into the animation.
func swarmMap(st *deploy.HostStats, rates map[string]peerRate, phase float64, w, h int) []string {
	h -= 2
	// As wide as the map needs at this height, if the width allows.
	gw, gh := (mapLonMax-mapLonMin)*mapKx, mapLatMax-mapLatMin
	w = min(w, int(math.Ceil(float64(4*h)*gw/gh/2)))
	c := newMapCanvas(w, h)
	fill := c.style(sFaint)
	neighbour := c.style(sFaint)
	border := c.style(sMuted)
	guess := c.style(lipgloss.NewStyle().Foreground(cAmber))
	link := c.style(lipgloss.NewStyle().Foreground(cPurple))
	live := c.style(lipgloss.NewStyle().Foreground(cPurple).Bold(true))
	pulseIn := c.style(lipgloss.NewStyle().Foreground(cMint).Bold(true))
	pulseOut := c.style(lipgloss.NewStyle().Foreground(cPink).Bold(true))
	label := c.style(sMuted)
	me := c.style(sAccent)
	peerIdle := c.style(sMuted)
	peerLive := c.style(lipgloss.NewStyle().Foreground(cMint).Bold(true))

	// Iran, filled with a light texture, and its neighbours' borders.
	for y := 2; y < 4*h; y += 4 {
		for x := (y / 4 % 2) * 2; x < 2*w; x += 4 { // every other cell, staggered
			lon := mapLonMin + (float64(x)-c.ox)/c.scale/mapKx
			lat := mapLatMax - (float64(y)-c.oy)/c.scale
			for _, s := range geo.Shapes {
				if s.Country == "IR" && inside(lon, lat, s.Ring) {
					c.dot(x, y, layerFill, fill)
					break
				}
			}
		}
	}
	for _, s := range geo.Shapes {
		layer, style := layerNeighbour, neighbour
		if s.Country == "IR" {
			layer, style = layerBorder, border
		}
		for i := range s.Ring {
			a, b := s.Ring[i], s.Ring[(i+1)%len(s.Ring)]
			x0, y0 := c.project(a[0], a[1])
			x1, y1 := c.project(b[0], b[1])
			c.line(x0, y0, x1, y1, 1, layer, style)
		}
	}

	// Where the node is, and its peers, grouped by town or country.
	var self *mapNode
	for _, a := range st.Self {
		if p, ok := geo.Locate(a); ok {
			self = c.place(p, "you")
			break
		}
	}
	if self == nil {
		x, y := c.project(iranLon, iranLat)
		self = &mapNode{x: x, y: y, label: "you (place unknown)"}
	}
	groups := map[string]*mapNode{}
	var nodes []*mapNode
	for _, p := range st.PeerList {
		where, ok := geo.Locate(p.Addr)
		key := where.Country + "/" + where.Town
		if !ok {
			key = "?"
		}
		n := groups[key]
		if n == nil {
			if ok {
				n = c.place(where, "")
			} else {
				x, y, arrow := c.edgePoint(c.project(iranLon, mapLatMin-10))
				n = &mapNode{x: x, y: y, abroad: true, edge: arrow, label: "?"}
			}
			groups[key] = n
			nodes = append(nodes, n)
		}
		n.peers++
		r := rates[p.ID]
		n.rate.in += r.in
		n.rate.out += r.out
	}
	sort.SliceStable(nodes, func(i, j int) bool { return nodes[i].peers > nodes[j].peers })
	// One marker a cell: a spot that lands on a taken cell moves to the
	// nearest free one, the busiest first.
	used := map[[2]int]bool{{int(self.x) / 2, int(self.y) / 4}: true}
	for _, n := range nodes {
		cx, cy := int(n.x)/2, int(n.y)/4
	search:
		for d := 0; d < 6; d++ {
			for dy := -d; dy <= d; dy++ {
				for dx := -d * 2; dx <= d*2; dx += 2 { // two apart sideways leaves room between
					at := [2]int{cx + dx, cy + dy}
					if (d == 0 || max(abs(dx)/2, abs(dy)) == d) && at[0] >= 0 && at[0] < w && at[1] >= 0 && at[1] < h && !used[at] {
						used[at] = true
						n.x, n.y = float64(at[0]*2)+0.5, float64(at[1]*4)+1.5
						break search
					}
				}
			}
		}
	}

	// Guessed links between peers: all of them in a small swarm, else each
	// to its two nearest.
	for i, a := range nodes {
		var near []*mapNode
		for j, b := range nodes {
			if j != i && (len(nodes) <= 12 && j > i || len(nodes) > 12) {
				near = append(near, b)
			}
		}
		if len(nodes) > 12 {
			sort.Slice(near, func(x, y int) bool {
				return math.Hypot(near[x].x-a.x, near[x].y-a.y) < math.Hypot(near[y].x-a.x, near[y].y-a.y)
			})
			near = near[:min(2, len(near))]
		}
		for _, b := range near {
			c.line(a.x, a.y, b.x, b.y, 3, layerGuess, guess)
		}
	}

	// The node's own links, and data moving along them.
	for _, n := range nodes {
		layer, style := layerLink, link
		if n.rate.in+n.rate.out > 0 {
			layer, style = layerLive, live
		}
		c.line(self.x, self.y, n.x, n.y, 1, layer, style)
		pulses := func(rate float64, fromX, fromY, toX, toY float64, layer, style int) {
			if rate <= 0 {
				return
			}
			k := int(math.Min(4, math.Max(1, 1+math.Log10(rate/1e3)))) // 1 to 4 a line
			length := math.Hypot(toX-fromX, toY-fromY)
			period := math.Max(1, length/24) // seconds to cross: about 24 dots a second
			for i := range k {
				t := math.Mod(phase/period+float64(i)/float64(k), 1)
				for tail := range 3 { // a head and a short tail behind it
					tt := t - float64(tail)*1.5/math.Max(length, 1)
					if tt < 0 {
						continue
					}
					c.dot(int(math.Round(fromX+(toX-fromX)*tt)), int(math.Round(fromY+(toY-fromY)*tt)), layer, style)
				}
			}
		}
		pulses(n.rate.in, n.x, n.y, self.x, self.y, layerPulseIn, pulseIn)
		pulses(n.rate.out, self.x, self.y, n.x, n.y, layerPulseOut, pulseOut)
	}

	// Markers, then labels where they fit.
	mark := func(n *mapNode, glyph string, style int) (int, int) {
		cx, cy := clampInt(int(n.x)/2, 0, w-1), clampInt(int(n.y)/4, 0, h-1)
		c.cells[cy*w+cx] = mapCell{layer: layerText, style: style, text: glyph}
		return cx, cy
	}
	for _, n := range nodes {
		glyph, style := "●", peerIdle
		if n.rate.in+n.rate.out > 0 {
			style = peerLive
		}
		if n.abroad {
			glyph = n.edge
		}
		cx, cy := mark(n, glyph, style)
		text := n.label
		if n.peers > 1 {
			text += fmt.Sprintf(" ×%d", n.peers)
		}
		if !c.put(cx+1, cy, text, layerPulseOut, label) && !c.put(cx-len([]rune(text)), cy, text, layerPulseOut, label) {
			c.put(cx-len([]rune(text))/2, cy+1, text, layerPulseOut, label)
		}
	}
	cx, cy := mark(self, "◆", me)
	if !c.put(cx+1, cy, self.label, layerPulseOut, me) {
		c.put(cx-len([]rune(self.label)), cy, self.label, layerPulseOut, me)
	}

	rows := c.render()
	key := func(st lipgloss.Style, glyph, what string) string { return st.Render(glyph) + sFaint.Render(" "+what) }
	rows = append(rows,
		key(sAccent, "◆", "this node")+"  "+key(sMuted, "●", "peer")+"  "+
			key(lipgloss.NewStyle().Foreground(cMint).Bold(true), "⠒", "coming in")+"  "+
			key(lipgloss.NewStyle().Foreground(cPink).Bold(true), "⠒", "going out")+"  "+key(lipgloss.NewStyle().Foreground(cAmber), "⠂⠂", "likely links, guessed"),
		sFaint.Render(geo.Credit+" · borders by Natural Earth"))
	return rows
}

// place puts a located address on the map: in Iran at its town, abroad on
// the edge toward its country.
func (c *mapCanvas) place(p geo.Place, label string) *mapNode {
	x, y := c.project(p.Lon, p.Lat)
	if p.Country == "IR" {
		x, y = math.Round(x/2)*2+0.5, math.Round(y/4)*4+1.5 // the middle of its cell
		if label == "" {
			label = p.Town
		}
		return &mapNode{x: x, y: y, label: label}
	}
	ex, ey, arrow := c.edgePoint(x, y)
	if label == "" {
		label = p.Country
	} else {
		label += " (" + p.Country + ")"
	}
	return &mapNode{x: ex, y: ey, label: label, abroad: true, edge: arrow}
}

func clampInt(v, lo, hi int) int { return max(lo, min(hi, v)) }

func abs(v int) int { return max(v, -v) }
