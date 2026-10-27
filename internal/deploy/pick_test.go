package deploy

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"interzone/internal/config"
)

func TestPickFor(t *testing.T) {
	fresh := "Date: " + time.Now().UTC().Format(time.RFC1123) + "\n"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/good/ubuntu/dists/noble-security/Release" {
			http.NotFound(w, r)
			return
		}
		w.Write([]byte(fresh))
	}))
	defer srv.Close()
	defer func(k []config.Mirror) { config.KnownMirrors = k }(config.KnownMirrors)
	config.KnownMirrors = []config.Mirror{
		{Kind: config.MirrorOS, URL: srv.URL + "/debian-only", Covers: []string{config.Debian}},
		{Kind: config.MirrorOS, URL: srv.URL + "/empty", Covers: []string{config.Ubuntu}},
		{Kind: config.MirrorOS, URL: srv.URL + "/good", Covers: []string{config.Ubuntu}},
	}
	needs := []need{{config.Ubuntu, "noble"}}

	for _, tc := range []struct{ set, want string }{
		{srv.URL + "/broken", srv.URL + "/good"}, // the set one lacks it: the first known one that has it
		{srv.URL + "/good", srv.URL + "/good"},
		{"", srv.URL + "/good"},
	} {
		c := config.Default()
		c.Sources.Blackout, c.Sources.APTMirror = true, tc.set
		r := &Runner{cfg: c, events: make(chan Event, 64)}
		r.pickFor(context.Background(), config.MirrorOS, &c.Sources.APTMirror, needs, "x86_64")
		if c.Sources.APTMirror != tc.want {
			t.Errorf("set %q: picked %q, want %q", tc.set, c.Sources.APTMirror, tc.want)
		}
		saved := false
		for len(r.events) > 0 {
			if (<-r.events).Sources != nil {
				saved = true
			}
		}
		if saved != (tc.set != tc.want) {
			t.Errorf("set %q: Sources event sent = %v", tc.set, saved)
		}
	}

	// nothing carries it: the set one stays
	c := config.Default()
	c.Sources.APTMirror = srv.URL + "/broken"
	r := &Runner{cfg: c, events: make(chan Event, 64)}
	r.pickFor(context.Background(), config.MirrorOS, &c.Sources.APTMirror, []need{{config.Ubuntu, "jammy"}}, "x86_64")
	if c.Sources.APTMirror != srv.URL+"/broken" {
		t.Errorf("with no mirror carrying it, picked %q", c.Sources.APTMirror)
	}
}
