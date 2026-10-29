package deploy

import (
	"slices"
	"testing"

	"interzone/internal/config"
)

func TestNewNotices(t *testing.T) {
	old := "# 2026-09-18\n\n## LiveKit JWT Service 0.7.0\n\nOld news.\n"
	cur := "# 2026-10-19\n\n## `just start-all` restarts again\n\nEveryone.\n\n" +
		"# 2026-09-22\n\n## LiveKit's TURN over TLS works\n\nThis only affects you if you have [LiveKit Server](./docs/configuring-playbook-livekit-server.md).\n\n" +
		"## Postmoogle moved\n\nSee [the docs](docs/configuring-playbook-bridge-postmoogle.md); Synapse is unaffected.\n\n" + old
	c := config.Default()
	c.Homeserver = "synapse"
	c.Components = map[string]bool{"matrix-rtc": true}
	ns := newNotices(old, cur, c)
	if len(ns) != 3 {
		t.Fatalf("got %d notices: %+v", len(ns), ns)
	}
	if ns[0].Date != "2026-10-19" || ns[0].Title != "`just start-all` restarts again" || ns[0].Yours != nil {
		t.Errorf("first: %+v", ns[0])
	}
	if !slices.Contains(ns[1].Yours, "Matrix RTC stack") || ns[1].Body != "This only affects you if you have LiveKit Server." {
		t.Errorf("LiveKit notice: %+v", ns[1])
	}
	if ns[2].Yours != nil {
		t.Errorf("Postmoogle is off and Synapse only mentioned, yet: %+v", ns[2])
	}
	if newNotices("", cur, c) != nil {
		t.Errorf("no old changelog should give no notices")
	}
}
