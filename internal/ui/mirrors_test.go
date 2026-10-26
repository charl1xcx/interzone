package ui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"interzone/internal/deploy"
)

// The images tab lists what the setup needs apart from the rest, and only
// the needed ones count towards the tile.
func TestMirrorsImagesNeededFirst(t *testing.T) {
	a := demoApp(t, 120, 36)
	openMirrors(a)
	ms := a.cur().(*mirrorsScreen)
	ms.res.ImgErr, ms.res.Arch = "", "x86_64"
	ms.res.Images = []deploy.ImageStatus{
		{Image: deploy.Image{Ref: "registry.example/jitsi/web:1"}, Detail: "not found"},
		{Image: deploy.Image{Ref: "registry.example/element-hq/synapse:1", Needed: true}, OK: true},
		{Image: deploy.Image{Ref: "registry.example/mautrix/telegram:1", Needed: true}, Detail: "not found"},
	}
	ms.show(mirrorsImages)
	if ok, n := ms.tally(mirrorsImages); ok != 1 || n != 2 {
		t.Errorf("tally %d/%d, want 1/2", ok, n)
	}
	v := ansi.Strip(a.View())
	needed, unused := strings.Index(v, "Needed by example.org · 2"), strings.Index(v, "Not used by your setup · 1")
	if needed < 0 || unused < needed {
		t.Fatalf("want the needed images above the unused ones:\n%s", v)
	}
	if tg, syn := strings.Index(v, "telegram"), strings.Index(v, "synapse"); tg < needed || tg > syn || syn > unused {
		t.Errorf("want the missing needed image first, then the rest, above the unused ones:\n%s", v)
	}
	if !strings.Contains(v[unused:], "jitsi") {
		t.Errorf("jitsi isn't under the unused ones:\n%s", v)
	}
}
