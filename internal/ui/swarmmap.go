package ui

import (
	"fmt"
	"maps"
	"math"
	"net/netip"
	"slices"
	"sort"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/lucasb-eyer/go-colorful"

	"interzone/internal/deploy"
	"interzone/internal/geo"
)

// ── The Host screen's swarm map ────────────────────────────────────
//
// Iran drawn in braille dots (2×4 to a cell), the hosting node and every
// peer it's connected to placed by their IPs (geo), a line to each, and
// comets running along them as bitswap moves data: in toward the node,
// out toward the peer. Peers abroad sit on the map's edge, toward their
// country. The node only knows its own connections; the faint dotted lines
// between peers are a guess (a small private swarm tends to connect every
// node to every other), and the legend says so.
//
// The big map (interzone-mirror's node, mapScene.big) adds provinces,
// cities and seas, a radar sweeping from the node, a breathing glow, and
// ripples where peers come and go.

// The part of the world Iran is fitted into, in degrees, and its middle.
// A box of another shape shows more of the region around it.
const (
	mapLonMin, mapLonMax = 43.0, 64.5
	mapLatMin, mapLatMax = 24.6, 40.1
	iranLon, iranLat     = 53.7, 32.4
)

var mapKx = math.Cos(32.4 * math.Pi / 180) // a degree of longitude is shorter here

// Layers: a cell takes the colour of the highest one drawn into it.
const (
	layerFill = iota + 1
	layerProvince
	layerNeighbour
	layerRadar
	layerBorder
	layerCity
	layerGuess
	layerLink
	layerLive
	layerRipple
	layerPulseIn
	layerPulseOut
	layerText
)

// peerRate is how fast bitswap moves data with a peer, bytes a second.
type peerRate struct{ in, out float64 }

// ripple is a ring spreading from where a peer came or went.
type ripple struct {
	addr  netip.Addr
	start float64 // the scene's phase when it began
	join  bool
}

const rippleSecs = 2.4

// mapScene is everything a frame of the map shows.
type mapScene struct {
	st      *deploy.HostStats
	rates   map[string]peerRate
	phase   float64 // seconds into the animation
	ripples []ripple
	big     bool       // the full-screen map: details and every animation
	home    *geo.Place // where the user says the node is, over its address's place
}

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
	styles  map[string]int
	scale   float64 // dots a degree (of latitude)
	ox, oy  float64 // dot offset of mapLonMin, mapLatMax
}

func newMapCanvas(w, h int) *mapCanvas {
	c := &mapCanvas{w: w, h: h, cells: make([]mapCell, w*h), styles: map[string]int{}}
	gw, gh := (mapLonMax-mapLonMin)*mapKx, mapLatMax-mapLatMin
	c.scale = min(float64(2*w)/gw, float64(4*h)/gh)
	c.ox = (float64(2*w) - gw*c.scale) / 2
	c.oy = (float64(4*h) - gh*c.scale) / 2
	return c
}

// style is the palette index of a colour, bold or not.
func (c *mapCanvas) style(col ac, bold bool) int {
	key := fmt.Sprint(col.Light, col.Dark, bold)
	if i, ok := c.styles[key]; ok {
		return i
	}
	c.palette = append(c.palette, lipgloss.NewStyle().Foreground(col).Bold(bold))
	c.styles[key] = len(c.palette) - 1
	return len(c.palette) - 1
}

type mixKey struct {
	a, b ac
	t    float64
}

var mixed = map[mixKey]ac{} // blending is slow, and a frame asks for the same few thousands of times

// mix is a between a and b: t 0 is a, 1 is b, in both of a theme's shades.
func mix(a, b ac, t float64) ac {
	t = math.Round(math.Max(0, math.Min(1, t))*8) / 8 // 9 steps, so the palette stays small
	k := mixKey{a, b, t}
	if m, ok := mixed[k]; ok {
		return m
	}
	blend := func(x, y string) string {
		cx, err1 := colorful.Hex(x)
		cy, err2 := colorful.Hex(y)
		if err1 != nil || err2 != nil {
			return x
		}
		return cx.BlendLab(cy, t).Clamped().Hex()
	}
	mixed[k] = ac{Light: blend(a.Light, b.Light), Dark: blend(a.Dark, b.Dark)}
	return mixed[k]
}

// project turns lon/lat into dot coordinates.
func (c *mapCanvas) project(lon, lat float64) (float64, float64) {
	return c.ox + (lon-mapLonMin)*mapKx*c.scale, c.oy + (mapLatMax-lat)*c.scale
}

func (c *mapCanvas) unproject(x, y float64) (float64, float64) {
	return mapLonMin + (x-c.ox)/c.scale/mapKx, mapLatMax - (y-c.oy)/c.scale
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

func (c *mapCanvas) dotf(x, y float64, layer, style int) {
	c.dot(int(math.Round(x)), int(math.Round(y)), layer, style)
}

// line draws from one dot to another, every step'th dot of it.
func (c *mapCanvas) line(x0, y0, x1, y1 float64, step, layer, style int) {
	n := int(math.Max(math.Abs(x1-x0), math.Abs(y1-y0)))
	for i := 0; i <= n; i += step {
		t := 0.0
		if n > 0 {
			t = float64(i) / float64(n)
		}
		c.dotf(x0+(x1-x0)*t, y0+(y1-y0)*t, layer, style)
	}
}

// free says whether text can go in a cell: nothing written there, and
// nothing above layer drawn.
func (c *mapCanvas) free(cx, cy, layer int) bool {
	if cx < 0 || cy < 0 || cx >= c.w || cy >= c.h {
		return false
	}
	cell := c.cells[cy*c.w+cx]
	return cell.text == "" && cell.layer <= layer
}

// put writes text from a cell rightwards, over anything up to layer.
func (c *mapCanvas) put(cx, cy int, s string, layer, style int) bool {
	r := []rune(s)
	cx = clampInt(cx, 0, c.w-len(r))
	if cy < 0 || cy >= c.h || cx < 0 {
		return false
	}
	for i := range r {
		if !c.free(cx+i, cy, layer) {
			return false
		}
	}
	for i, ch := range r {
		c.cells[cy*c.w+cx+i] = mapCell{layer: layerText, style: style, text: string(ch)}
	}
	return true
}

// label puts text beside a cell: right of it, else left, else below.
func (c *mapCanvas) label(cx, cy int, s string, layer, style int) bool {
	n := len([]rune(s))
	return c.put(cx+1, cy, s, layer, style) || c.put(cx-n, cy, s, layer, style) || c.put(cx-n/2, cy+1, s, layer, style)
}

func (c *mapCanvas) render() []string {
	// Each style's codes, from rendering a stand-in once: Render per run is slow.
	wrap := make([][2]string, len(c.palette))
	for i, st := range c.palette {
		before, after, _ := strings.Cut(st.Render("\x00"), "\x00")
		wrap[i] = [2]string{before, after}
	}
	rows := make([]string, c.h)
	for y := range c.h {
		var b, run strings.Builder
		runStyle := -1
		flush := func() {
			if run.Len() == 0 {
				return
			}
			if runStyle < 0 {
				b.WriteString(run.String())
			} else {
				b.WriteString(wrap[runStyle][0] + run.String() + wrap[runStyle][1])
			}
			run.Reset()
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
			run.WriteString(ch)
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

func inIran(lon, lat float64) bool {
	for _, s := range geo.Shapes {
		if s.Country == "IR" && inside(lon, lat, s.Ring) {
			return true
		}
	}
	return false
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
	lit    float64 // how brightly the radar lights it, 0 to 1
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

// placeAddr is where an address goes, "?" on the bottom edge if nowhere,
// and the key of its group.
func (c *mapCanvas) placeAddr(a netip.Addr) (*mapNode, string) {
	if p, ok := geo.Locate(a); ok {
		return c.place(p, ""), p.Country + "/" + p.Town
	}
	x, y, arrow := c.edgePoint(c.project(iranLon, mapLatMin-10))
	return &mapNode{x: x, y: y, abroad: true, edge: arrow, label: "?"}, "?"
}

var land struct {
	key string
	c   *mapCanvas
}

// landCanvas is a canvas with the land drawn, which doesn't move: it is
// drawn once for a size and theme and copied for each frame.
func landCanvas(w, h int, big bool) *mapCanvas {
	key := fmt.Sprint(w, h, big, cFaint, cMuted, cPurple, cPink, cMint)
	if land.key != key {
		land.key, land.c = key, drawLand(w, h, big)
	}
	b := land.c
	return &mapCanvas{w: b.w, h: b.h, cells: slices.Clone(b.cells), palette: slices.Clone(b.palette),
		styles: maps.Clone(b.styles), scale: b.scale, ox: b.ox, oy: b.oy}
}

func drawLand(w, h int, big bool) *mapCanvas {
	c := newMapCanvas(w, h)
	// The land: Iran filled with a light texture, its provinces, its
	// neighbours, and its border coloured along the theme's ramp.
	fill := c.style(cFaint, false)
	for y := 2; y < 4*h; y += 4 {
		for x := (y / 4 % 2) * 2; x < 2*w; x += 4 { // every other cell, staggered
			if inIran(c.unproject(float64(x), float64(y))) {
				c.dot(x, y, layerFill, fill)
			}
		}
	}
	ring := func(r [][2]float64, step, layer int, col func(t float64) int) {
		for i := range r {
			a, b := r[i], r[(i+1)%len(r)]
			x0, y0 := c.project(a[0], a[1])
			x1, y1 := c.project(b[0], b[1])
			c.line(x0, y0, x1, y1, step, layer, col(float64(i)/float64(len(r))))
		}
	}
	if big {
		prov := c.style(mix(cFaint, cMuted, 0.25), false)
		for _, p := range geo.Provinces {
			ring(p.Ring, 2, layerProvince, func(float64) int { return prov })
		}
	}
	neighbour := c.style(cFaint, false)
	for _, s := range geo.Shapes {
		if s.Country == "IR" {
			// The ramp goes there and back, so it has no seam.
			ring(s.Ring, 1, layerBorder, func(t float64) int {
				t = 1 - math.Abs(2*t-1)
				if t < 0.5 {
					return c.style(mix(cPurple, cPink, t*2), false)
				}
				return c.style(mix(cPink, cMint, t*2-1), false)
			})
		} else {
			ring(s.Ring, 1, layerNeighbour, func(float64) int { return neighbour })
		}
	}

	return c
}

// swarmMap draws a scene in w×h cells: the map, then two rows of legend.
func swarmMap(sc mapScene, w, h int) []string {
	h -= 2
	if !sc.big {
		// As wide as the map needs at this height, if the width allows.
		gw, gh := (mapLonMax-mapLonMin)*mapKx, mapLatMax-mapLatMin
		w = min(w, int(math.Ceil(float64(4*h)*gw/gh/2)))
	}
	c := landCanvas(w, h, sc.big)
	st := sc.st
	breath := (1 + math.Sin(sc.phase*2*math.Pi/3)) / 2 // 0 to 1, every 3 seconds

	// Where the node is, and its peers, grouped by town or country.
	var self *mapNode
	if sc.home != nil {
		self = c.place(*sc.home, "you")
	}
	for _, a := range st.Self {
		if self != nil {
			break
		}
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
		n0, key := c.placeAddr(p.Addr)
		n := groups[key]
		if n == nil {
			n = n0
			groups[key] = n
			nodes = append(nodes, n)
		}
		n.peers++
		r := sc.rates[p.ID]
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

	// The radar: a beam turning round the node every 6 seconds, fading
	// behind; a peer it passes lights up and dims again.
	if sc.big {
		const turn = 6.0
		angle := math.Mod(sc.phase/turn, 1) * 2 * math.Pi
		reach := math.Hypot(float64(2*w), float64(4*h))
		for i, fade := range []float64{0.35, 0.55, 0.72, 0.86} {
			a := angle - float64(i)*0.07
			col := c.style(mix(cPurple, cFaint, fade), false)
			c.line(self.x, self.y, self.x+math.Cos(a)*reach, self.y+math.Sin(a)*reach, 2, layerRadar, col)
		}
		for _, n := range nodes {
			behind := math.Mod(angle-math.Atan2(n.y-self.y, n.x-self.x)+4*math.Pi, 2*math.Pi) // radians since the beam passed
			n.lit = math.Max(0, 1-behind/1.2)
		}
	}

	// Cities, faint, under everything that matters.
	if sc.big {
		city := c.style(cMuted, false)
		for _, l := range geo.Cities {
			x, y := c.project(l.Lon, l.Lat)
			c.dotf(x, y, layerCity, city)
		}
	}

	// Guessed links between peers: all of them in a small swarm, else each
	// to its two nearest.
	guess := c.style(mix(cAmber, cFaint, 0.25), false)
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

	// The node's own links, and comets along them as data moves: more,
	// longer and faster with more traffic, their tails fading.
	link := c.style(mix(cPurple, cFaint, 0.35), false)
	live := c.style(cPurple, false)
	for _, n := range nodes {
		layer, style := layerLink, link
		if n.rate.in+n.rate.out > 0 {
			layer, style = layerLive, live
		}
		c.line(self.x, self.y, n.x, n.y, 1, layer, style)
		comets := func(rate float64, fromX, fromY, toX, toY float64, layer int, col ac) {
			if rate <= 0 {
				return
			}
			size := math.Max(0, math.Log10(rate/1e3)) // 0 at 1 kB/s, 3 at 1 MB/s
			k := int(math.Min(4, 1+size))
			tail := 2 + int(math.Min(8, size*2.5))
			length := math.Max(1, math.Hypot(toX-fromX, toY-fromY))
			speed := 18 + 8*size // dots a second
			for i := range k {
				t := math.Mod(sc.phase*speed/length+float64(i)/float64(k), 1)
				for j := tail; j >= 0; j-- {
					tt := t - float64(j)/length
					if tt < 0 {
						continue
					}
					style := c.style(mix(col, cFaint, float64(j)/float64(tail+1)), j == 0)
					c.dotf(fromX+(toX-fromX)*tt, fromY+(toY-fromY)*tt, layer, style)
				}
			}
		}
		comets(n.rate.in, n.x, n.y, self.x, self.y, layerPulseIn, cMint)
		comets(n.rate.out, self.x, self.y, n.x, n.y, layerPulseOut, cPink)
	}

	// Ripples where peers came (mint) or went (red): two rings, fading.
	for _, r := range sc.ripples {
		age := sc.phase - r.start
		if age < 0 || age > rippleSecs {
			continue
		}
		n, _ := c.placeAddr(r.addr)
		col := cMint
		if !r.join {
			col = cRed
		}
		for ring := range 2 {
			rad := (age - float64(ring)*0.5) * 9 // dots
			if rad <= 0 {
				continue
			}
			style := c.style(mix(col, cFaint, age/rippleSecs+float64(ring)*0.2), false)
			for a := 0.0; a < 2*math.Pi; a += 1 / rad {
				c.dotf(n.x+math.Cos(a)*rad, n.y+math.Sin(a)*rad, layerRipple, style)
			}
		}
	}

	// Markers, then labels where they fit: the peers', the node's, then
	// the cities' and seas' around them.
	mark := func(n *mapNode, glyph string, style int) (int, int) {
		cx, cy := clampInt(int(n.x)/2, 0, w-1), clampInt(int(n.y)/4, 0, h-1)
		c.cells[cy*w+cx] = mapCell{layer: layerText, style: style, text: glyph}
		return cx, cy
	}
	glow := 0.0
	if sc.big {
		glow = 0.5 * (1 - breath)
	}
	me := c.style(mix(cPurple, cFaint, glow), true)
	sx, sy := mark(self, "◆", me)
	label := c.style(cMuted, false)
	for _, n := range nodes {
		glyph, col, bold := "●", mix(cMuted, cFaint, 0.2), false
		if n.rate.in+n.rate.out > 0 {
			col, bold = mix(cMint, cFaint, glow), true
		}
		if n.lit > 0 {
			col, bold = mix(cText, col, 1-n.lit), true
		}
		if n.abroad {
			glyph = n.edge
		}
		cx, cy := mark(n, glyph, c.style(col, bold))
		text := n.label
		if n.peers > 1 {
			text += fmt.Sprintf(" ×%d", n.peers)
		}
		c.label(cx, cy, text, layerPulseOut, label)
	}
	c.label(sx, sy, self.label, layerPulseOut, me)
	if sc.big {
		cityText := c.style(mix(cMuted, cFaint, 0.4), false)
		for _, l := range geo.Cities {
			x, y := c.project(l.Lon, l.Lat)
			c.put(int(x)/2+1, int(y)/4, l.Name, layerGuess, cityText)
		}
		sea := c.style(mix(cPurple, cFaint, 0.5), false)
		for _, l := range geo.Seas {
			x, y := c.project(l.Lon, l.Lat)
			c.put(int(x)/2-len([]rune(l.Name))/2, int(y)/4, l.Name, layerRadar, sea)
		}
	}

	rows := c.render()
	key := func(col ac, glyph, what string) string {
		return lipgloss.NewStyle().Foreground(col).Bold(true).Render(glyph) + sFaint.Render(" "+what)
	}
	rows = append(rows,
		truncate(key(cPurple, "◆", "this node")+"  "+key(cMuted, "●", "peer")+"  "+key(cMint, "⠒", "coming in")+"  "+
			key(cPink, "⠒", "going out")+"  "+key(cAmber, "⠂⠂", "guessed links"), w),
		truncate(sFaint.Render(geo.Credit+" · borders by Natural Earth"), w))
	for _, i := range []int{len(rows) - 2, len(rows) - 1} {
		rows[i] += strings.Repeat(" ", max(0, w-ansi.StringWidth(rows[i])))
	}
	return rows
}

func clampInt(v, lo, hi int) int { return max(lo, min(hi, v)) }

func abs(v int) int { return max(v, -v) }
