package ui

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"interzone/internal/config"
)

// ── Cache ──────────────────────────────────────────────────────────
//
// What the Dashboard, Users and the logs picker last loaded from a server
// is kept on disk per server, so opening them shows it at once while the
// fresh copy loads.

type cached[T any] struct {
	Taken time.Time
	Data  T
}

func cachePath(name string) string {
	return filepath.Join(config.Dir(), "cache", config.Active(), name+".json")
}

// loadCache reads what was saved under name for the active server.
func loadCache[T any](name string) (data T, taken time.Time, ok bool) {
	b, err := os.ReadFile(cachePath(name))
	if err != nil {
		return data, taken, false
	}
	var c cached[T]
	if json.Unmarshal(b, &c) != nil {
		return data, taken, false
	}
	return c.Data, c.Taken, true
}

func saveCache[T any](name string, data T) {
	b, err := json.Marshal(cached[T]{time.Now(), data})
	if err != nil {
		return
	}
	p := cachePath(name)
	if os.MkdirAll(filepath.Dir(p), 0o700) == nil {
		_ = os.WriteFile(p, b, 0o600)
	}
}

// ago says how long ago t was, roughly: "just now", "5m ago", "3h ago".
func ago(t time.Time) string {
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d/time.Minute))
	case d < 48*time.Hour:
		return fmt.Sprintf("%dh ago", int(d/time.Hour))
	}
	return fmt.Sprintf("%dd ago", int(d/(24*time.Hour)))
}
