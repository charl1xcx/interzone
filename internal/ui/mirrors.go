package ui

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"interzone/internal/config"
	"interzone/internal/deploy"
)

// ── Mirrors ────────────────────────────────────────────────────────
//
// Whether everything a deploy downloads is there: the playbook and each
// role it pins (at that version, from the roles mirror), the package
// mirrors and the registry, and every container image the playbook can
// deploy, the ones the enabled services use first. Each part is checked on
// its own, so the quick ones show while the images are still being worked
// out.

const (
	mirrorsRepos = iota
	mirrorsSources
	mirrorsImages
)

var mirrorsTabs = []string{"Repositories", "Mirrors", "Images"}

// mirrorsCache is where the last check is kept. Before images were marked
// needed or not they were kept under "mirrors".
const mirrorsCache = "mirrors-2"

type mirrorsResult struct {
	Repos   []deploy.Check
	Sources []deploy.Check
	Known   []deploy.MirrorResult
	IPFS    []deploy.Check
	Images  []deploy.ImageStatus
	Arch    string
	ImgErr  string
}

type mirrorsMsg struct {
	server string
	part   int
	res    mirrorsResult
}

func (mirrorsMsg) owner() string { return "Mirrors" }

type mirrorsScreen struct {
	cfg     *config.Config
	server  string
	res     mirrorsResult
	taken   time.Time
	tab     int
	loading [3]bool
	spin    spinner.Model
	scroll  int
	narrow  bool
}

func newMirrorsScreen() *mirrorsScreen { return &mirrorsScreen{spin: newSpinner()} }

func (s *mirrorsScreen) name() string         { return "Mirrors" }
func (s *mirrorsScreen) title() string        { return "Is everything there to download?" }
func (s *mirrorsScreen) typing() bool         { return false }
func (s *mirrorsScreen) leave(*config.Config) {}
func (s *mirrorsScreen) setCompact(on bool)   { s.narrow = on }
func (s *mirrorsScreen) keys() [][2]string {
	return [][2]string{{"←→", "repos · mirrors · images"}, {"r", "check again"}, {"↑↓", "scroll"}}
}

// show opens a tab, as the sidebar's entries do.
func (s *mirrorsScreen) show(tab int) { s.tab, s.scroll = tab, 0 }

func (s *mirrorsScreen) enter(c *config.Config) tea.Cmd {
	s.cfg = c
	if srv := config.Active(); srv != s.server {
		s.server, s.res, s.scroll = srv, mirrorsResult{}, 0
		if r, t, ok := loadCache[mirrorsResult](mirrorsCache); ok {
			s.res, s.taken = r, t
		}
		return s.check()
	}
	if s.busy() || time.Since(s.taken) < 2*time.Minute {
		return nil
	}
	return s.check()
}

func (s *mirrorsScreen) busy() bool { return s.loading[0] || s.loading[1] || s.loading[2] }

func (s *mirrorsScreen) check() tea.Cmd {
	if s.busy() {
		return nil
	}
	s.loading = [3]bool{true, true, true}
	cfg, srv := *s.cfg.Clone(), s.server
	part := func(n int, fn func(r *mirrorsResult)) tea.Cmd {
		return func() tea.Msg {
			var r mirrorsResult
			fn(&r)
			return mirrorsMsg{srv, n, r}
		}
	}
	return tea.Batch(s.spin.Tick,
		part(mirrorsRepos, func(r *mirrorsResult) { r.Repos = deploy.CheckRepos(&cfg) }),
		part(mirrorsSources, func(r *mirrorsResult) {
			done := make(chan []deploy.MirrorResult)
			swarm := make(chan []deploy.Check)
			go func() { done <- deploy.ProbeKnownMirrors() }()
			go func() { swarm <- deploy.ProbeIPFS() }()
			r.Sources = deploy.CheckSources(&cfg)
			r.Known, r.IPFS = <-done, <-swarm
		}),
		part(mirrorsImages, func(r *mirrorsResult) {
			if !cfg.Installed() {
				r.ImgErr = "the images are worked out from the playbook on the server; set it up first"
				return
			}
			ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
			defer cancel()
			imgs, arch, err := deploy.ListImages(ctx, &cfg, true)
			if err != nil {
				r.ImgErr = err.Error()
				return
			}
			r.Images, r.Arch = deploy.CheckImages(ctx, imgs, arch), arch
		}))
}

func (s *mirrorsScreen) update(msg tea.Msg) tea.Cmd {
	switch m := msg.(type) {
	case mirrorsMsg:
		if m.server != s.server {
			return nil
		}
		s.loading[m.part] = false
		switch m.part {
		case mirrorsRepos:
			s.res.Repos = m.res.Repos
		case mirrorsSources:
			s.res.Sources, s.res.Known, s.res.IPFS = m.res.Sources, m.res.Known, m.res.IPFS
		case mirrorsImages:
			s.res.ImgErr = m.res.ImgErr
			if m.res.ImgErr == "" {
				s.res.Images, s.res.Arch = m.res.Images, m.res.Arch
			}
		}
		if !s.busy() {
			s.taken = time.Now()
			saveCache(mirrorsCache, s.res)
		}
	case spinner.TickMsg:
		return spinning(s.busy(), &s.spin, m)
	case tea.KeyMsg:
		switch m.String() {
		case "r":
			return s.check()
		case "right", "l", "tab":
			s.show((s.tab + 1) % len(mirrorsTabs))
		case "left", "h", "shift+tab":
			s.show((s.tab + len(mirrorsTabs) - 1) % len(mirrorsTabs))
		case "1", "2", "3":
			s.show(int(m.String()[0] - '1'))
		case "up", "k":
			s.scroll = max(0, s.scroll-1)
		case "down", "j":
			s.scroll++
		}
	}
	return nil
}

func (s *mirrorsScreen) click(id string) (tea.Cmd, bool) {
	if n, ok := strings.CutPrefix(id, "mirrors:tab:"); ok {
		i, _ := strconv.Atoi(n)
		s.show(i)
		return nil, true
	}
	return nil, false
}

// tally is how many of a part answered, and how many were checked.
func (s *mirrorsScreen) tally(part int) (ok, n int) {
	switch part {
	case mirrorsRepos:
		for _, c := range s.res.Repos {
			n++
			if c.OK {
				ok++
			}
		}
	case mirrorsSources:
		for _, c := range s.res.Sources {
			n++
			if c.OK {
				ok++
			}
		}
		for _, c := range s.res.Known {
			n++
			if c.OK {
				ok++
			}
		}
		for _, c := range s.res.IPFS {
			n++
			if c.OK {
				ok++
			}
		}
	case mirrorsImages: // the ones the setup needs
		for _, c := range s.res.Images {
			if !c.Needed {
				continue
			}
			n++
			if c.OK {
				ok++
			}
		}
	}
	return
}

func (s *mirrorsScreen) tile(part int) func(w, h int) string {
	return func(w, h int) string {
		ok, n := s.tally(part)
		inner := max(4, w-4)
		value, note, style := fmt.Sprintf("%d/%d", ok, n), "all there", sGood
		switch {
		case s.loading[part] && n == 0:
			value, note = s.spin.View(), "checking…"
		case part == mirrorsImages && s.res.ImgErr != "" && n == 0:
			value, note, style = "–", "not checked", sWarn
		case n == 0:
			note = "nothing to check"
		case ok < n:
			note, style = fmt.Sprintf("%d missing", n-ok), sBad
		}
		if part == mirrorsImages && ok == n && n > 0 {
			if miss := s.unusedMissing(); miss > 0 {
				note = fmt.Sprintf("needed all there · %d unused missing", miss)
			}
		}
		if s.loading[part] && n > 0 {
			note = s.spin.View() + " checking again"
		}
		frac := 0.0
		if n > 0 {
			frac = float64(ok) / float64(n)
		}
		border := lipgloss.TerminalColor(cFaint)
		label := sMuted.Render(fmt.Sprintf("%d  %s", part+1, mirrorsTabs[part]))
		if part == s.tab {
			border, label = cPurple, sAccent.Render(fmt.Sprintf("%d  %s", part+1, mirrorsTabs[part]))
		}
		if style.GetForeground() == sBad.GetForeground() && part != s.tab {
			border = cRed
		}
		return mark("mirrors:tab:"+strconv.Itoa(part), card("", []string{
			label,
			sTitle.Render(value) + "  " + style.Render(truncate(note, max(1, inner-lipgloss.Width(value)-2))),
			bar(frac, inner, style),
		}, w, h, border))
	}
}

func (s *mirrorsScreen) view(w, h int) string {
	var top string
	if !s.narrow && w >= 60 {
		top = columns(w, s.tile(0), s.tile(1), s.tile(2))
	} else {
		var tabs []string
		for i, t := range []string{"Repos", "Mirrors", "Images"} {
			ok, n := s.tally(i)
			count := fmt.Sprintf("%d/%d", ok, n)
			switch {
			case s.loading[i]:
				count = s.spin.View()
			case n == 0:
				count = "–"
			}
			st := sMuted
			if ok < n {
				st = sBad
			}
			label := sMuted.Render(t) + " " + st.Render(count)
			if i == s.tab {
				label = sAccent.Render("▌"+t) + " " + st.Render(count)
			}
			tabs = append(tabs, mark("mirrors:tab:"+strconv.Itoa(i), label))
		}
		top = strings.Join(tabs, sFaint.Render("  ·  "))
		if lipgloss.Width(top) > w {
			top = strings.Join(tabs, "\n")
		}
	}
	when := ""
	switch {
	case s.busy():
		when = s.spin.View() + sFaint.Render(" checking")
		if !s.taken.IsZero() {
			when += sFaint.Render(" · last " + ago(s.taken))
		}
	case !s.taken.IsZero():
		when = sFaint.Render("checked " + ago(s.taken) + " · r checks again")
	}

	var rows []string
	switch s.tab {
	case mirrorsRepos:
		rows = s.repoRows(w)
	case mirrorsSources:
		rows = s.sourceRows(w)
	case mirrorsImages:
		rows = s.imageRows(w)
	}
	head := top + "\n" + when + "\n"
	room := max(3, h-lipgloss.Height(head))
	s.scroll = min(s.scroll, max(0, len(rows)-room))
	end := min(len(rows), s.scroll+room)
	shown := append([]string(nil), rows[s.scroll:end]...)
	if end < len(rows) && len(shown) > 0 {
		shown[len(shown)-1] = sFaint.Render(fmt.Sprintf("  ↓ %d more · ↑↓ scroll", len(rows)-end))
	}
	return head + strings.Join(shown, "\n")
}

// unusedMissing is how many images the setup doesn't use can't be pulled.
func (s *mirrorsScreen) unusedMissing() (n int) {
	for _, c := range s.res.Images {
		if !c.Needed && !c.OK {
			n++
		}
	}
	return
}

// checkRow is one checked thing: icon, name, where, and what was found.
func checkRow(w int, ok bool, name, target, detail string) []string {
	icon, st := sGood.Render("✓"), sMuted
	if !ok {
		icon, st = sBad.Render("✗"), sBad
	}
	if w < 70 {
		out := []string{icon + " " + sTitle.Render(truncate(name, w-2)) + "  " + st.Render(truncate(detail, max(4, w-lipgloss.Width(name)-4)))}
		if target != "" {
			out = append(out, "  "+sFaint.Render(truncateMid(target, w-2)))
		}
		return out
	}
	nameW := min(26, max(14, w/5))
	detW := min(50, max(14, w/3))
	return []string{icon + " " + lipgloss.NewStyle().Width(nameW).Render(sTitle.Render(truncate(name, nameW-1))) +
		lipgloss.NewStyle().Width(detW).Render(st.Render(truncate(detail, detW-1))) +
		sFaint.Render(truncateMid(target, max(8, w-nameW-detW-2)))}
}

// failuresFirst sorts the failed ones up, keeping the order otherwise.
func failuresFirst(n int, ok func(i int) bool) []int {
	idx := make([]int, n)
	for i := range idx {
		idx[i] = i
	}
	sort.SliceStable(idx, func(a, b int) bool { return !ok(idx[a]) && ok(idx[b]) })
	return idx
}

func (s *mirrorsScreen) waiting(part int, what string) []string {
	if s.loading[part] {
		return []string{s.spin.View() + sMuted.Render(" "+what)}
	}
	return nil
}

func (s *mirrorsScreen) repoRows(w int) []string {
	rs := s.res.Repos
	if len(rs) == 0 {
		return s.waiting(mirrorsRepos, "asking the git servers…")
	}
	base := s.cfg.Sources.RolesGitBase
	if base == "" {
		base = config.UpstreamGitHub
	}
	rows := strings.Split(sMuted.Width(w).Render("The playbook, and the roles it pins at their versions from "+base), "\n")
	rows = append(rows, "")
	if len(rs) <= 2 {
		rows = append(rows, sFaint.Render("The playbook isn't downloaded yet, so its roles aren't known; only the mirror is checked."), "")
	}
	for _, i := range failuresFirst(len(rs), func(i int) bool { return rs[i].OK }) {
		rows = append(rows, checkRow(w, rs[i].OK, rs[i].Name, rs[i].Target, rs[i].Detail)...)
	}
	return rows
}

func (s *mirrorsScreen) sourceRows(w int) []string {
	src, known := s.res.Sources, s.res.Known
	if len(src) == 0 && len(known) == 0 {
		return s.waiting(mirrorsSources, "probing the mirrors…")
	}
	rows := []string{sectionHeading("Used by "+s.cfg.Domain, min(w, 80))}
	switch {
	case s.cfg.Sources.IPFS:
		rows = append(rows, sFaint.Render("  everything comes from interzone's IPFS swarm, checked below"))
	case len(src) == 0:
		rows = append(rows, sFaint.Render("  no registry or package mirror is set; the upstream ones are used"))
	}
	for _, i := range failuresFirst(len(src), func(i int) bool { return src[i].OK }) {
		rows = append(rows, checkRow(w, src[i].OK, src[i].Name, src[i].Target, src[i].Detail)...)
	}
	rows = append(rows, "", sectionHeading("Known mirrors", min(w, 80)))
	kinds := map[config.MirrorKind]string{config.MirrorOS: "OS", config.MirrorDocker: "Docker", config.MirrorPyPI: "PyPI"}
	for _, i := range failuresFirst(len(known), func(i int) bool { return known[i].OK }) {
		k := known[i]
		detail := k.Detail
		if k.OK && k.Took > 0 {
			detail += fmt.Sprintf(" · %dms", k.Took.Milliseconds())
		}
		rows = append(rows, checkRow(w, k.OK, kinds[k.Mirror.Kind]+" · "+k.Mirror.Name, k.Mirror.URL, detail)...)
	}
	if len(s.res.IPFS) > 0 {
		rows = append(rows, "", sectionHeading("interzone's IPFS swarm", min(w, 80)))
		for _, c := range s.res.IPFS {
			rows = append(rows, checkRow(w, c.OK, c.Name, c.Target, c.Detail)...)
		}
	}
	return rows
}

func (s *mirrorsScreen) imageRows(w int) []string {
	imgs := s.res.Images
	var rows []string
	if s.res.ImgErr != "" {
		rows = append(rows, sWarn.Render("! "+s.res.ImgErr), "")
	}
	if len(imgs) == 0 {
		if r := s.waiting(mirrorsImages, "asking the playbook which images it needs (this takes a minute)…"); r != nil {
			return append(rows, r...)
		}
		return rows
	}
	where := "the registries"
	if s.cfg.Sources.UseRegistry && s.cfg.Sources.RegistryPrefix != "" {
		where = s.cfg.Sources.RegistryPrefix
	}
	rows = append(rows, sMuted.Render(fmt.Sprintf("%d images for %s, from ", len(imgs), s.res.Arch))+sTitle.Render(truncateMid(where, max(10, w-40))), "")
	for _, needed := range []bool{true, false} {
		var group []deploy.ImageStatus
		for _, im := range imgs {
			if im.Needed == needed {
				group = append(group, im)
			}
		}
		title := fmt.Sprintf("Needed by %s · %d", s.cfg.Domain, len(group))
		if !needed {
			title = fmt.Sprintf("Not used by your setup · %d", len(group))
			rows = append(rows, "")
		}
		rows = append(rows, sectionHeading(title, min(w, 80)))
		if len(group) == 0 {
			rows = append(rows, sFaint.Render("  none"))
		}
		for _, i := range failuresFirst(len(group), func(i int) bool { return group[i].OK }) {
			im := group[i]
			host, repo, tag := deploy.ParseRef(im.Ref)
			detail := tag
			if !im.OK {
				detail = im.Detail
			}
			name := repo
			if j := strings.LastIndex(name, "/"); j >= 0 && w < 100 {
				name = name[j+1:]
			}
			rows = append(rows, checkRow(w, im.OK, name, host+"/"+repo+":"+tag, detail)...)
		}
	}
	return rows
}
