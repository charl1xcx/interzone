package deploy

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"time"
	"unicode"

	"interzone/internal/config"
)

// Room is a room the homeserver knows of, as Synapse's admin API lists it.
type Room struct {
	ID, Name, Alias, Creator string
	Members, Local           int // joined, and of those on this server
	Events                   int // state events: what Synapse sorts rooms by size with
	Public, Encrypted        bool
	Federated, Blocked       bool
	Created                  time.Time // zero if the database couldn't say
	MediaCount               int       // files its messages link to; -1 if unknown
	MediaBytes               int64     // their size on this server's disk
	DataBytes                int64     // its share of the database; -1 if unknown
}

// Bytes is all the room takes on this server's disk, as far as is known:
// its share of the database and its media; 0 if unknown.
func (r Room) Bytes() int64 { return max(0, r.DataBytes) + r.MediaBytes }

// Title is the room's name, else its alias, else its ID.
func (r Room) Title() string {
	switch {
	case r.Name != "":
		return r.Name
	case r.Alias != "":
		return r.Alias
	}
	return r.ID
}

// Printable makes text anyone on Matrix can set safe to draw: control
// characters (escapes, tabs, newlines) become spaces, and zero-width joiners
// go: terminals disagree on how wide a joined emoji is, and a line drawn
// wider than planned wraps and garbles the screen.
func Printable(s string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r == '\u200d':
			return -1
		case unicode.IsControl(r):
			return ' '
		}
		return r
	}, s)
}

// ListRooms returns every room on the server, biggest first.
func ListRooms(ctx context.Context, c *config.Config, login *AdminLogin) ([]Room, error) {
	out, err := adminCall(ctx, c, login, map[string]any{"op": "rooms"})
	if err != nil {
		return nil, err
	}
	return parseRooms(out)
}

func parseRooms(out []byte) ([]Room, error) {
	var v struct {
		Rooms []struct {
			ID        string  `json:"room_id"`
			Name      *string `json:"name"`
			Alias     *string `json:"canonical_alias"`
			Creator   string  `json:"creator"`
			Members   int     `json:"joined_members"`
			Local     int     `json:"joined_local_members"`
			Events    int     `json:"state_events"`
			Public    bool    `json:"public"`
			Encrypted *string `json:"encryption"`
			Federated bool    `json:"federatable"`
			Blocked   bool    `json:"blocked"`
			Created   int64   `json:"created"`
			Media     []int64 `json:"media"`
			Data      *int64  `json:"data"`
		} `json:"rooms"`
	}
	if err := json.Unmarshal(out, &v); err != nil {
		return nil, fmt.Errorf("couldn't read the room list: %w", err)
	}
	str := func(s *string) string {
		if s == nil {
			return ""
		}
		return *s
	}
	rooms := make([]Room, 0, len(v.Rooms))
	for _, r := range v.Rooms {
		rooms = append(rooms, Room{ID: r.ID, Name: Printable(str(r.Name)), Alias: Printable(str(r.Alias)), Creator: Printable(r.Creator),
			Members: r.Members, Local: r.Local, Events: r.Events, Public: r.Public,
			Encrypted: str(r.Encrypted) != "", Federated: r.Federated, Blocked: r.Blocked, MediaCount: -1, DataBytes: -1})
		if rm := &rooms[len(rooms)-1]; len(r.Media) == 2 {
			rm.Created, rm.MediaCount, rm.MediaBytes = msTime(r.Created), int(r.Media[0]), r.Media[1]
		}
		if r.Data != nil {
			rooms[len(rooms)-1].DataBytes = *r.Data
		}
	}
	return rooms, nil
}

func roomCall(ctx context.Context, c *config.Config, login *AdminLogin, method, path string, body map[string]any) error {
	_, err := adminCall(ctx, c, login, map[string]any{"op": "call", "method": method, "path": path, "body": body})
	return err
}

// BlockRoom stops (or lets) people on this server join the room.
func BlockRoom(ctx context.Context, c *config.Config, login *AdminLogin, id string, block bool) error {
	return roomCall(ctx, c, login, "PUT", "/_synapse/admin/v1/rooms/"+url.PathEscape(id)+"/block", map[string]any{"block": block})
}

// DeleteRoom makes everyone on this server leave the room, removes it from
// the database and blocks it, so it can't be joined again. Synapse finishes
// in the background.
func DeleteRoom(ctx context.Context, c *config.Config, login *AdminLogin, id string) error {
	return roomCall(ctx, c, login, "DELETE", "/_synapse/admin/v2/rooms/"+url.PathEscape(id), map[string]any{"block": true, "purge": true})
}

// PurgeHistory deletes the room's messages from before the given time from
// this server's database; messages sent from this server are kept, as other
// servers may not have them.
func PurgeHistory(ctx context.Context, c *config.Config, login *AdminLogin, id string, before time.Time) error {
	return roomCall(ctx, c, login, "POST", "/_synapse/admin/v1/purge_history/"+url.PathEscape(id),
		map[string]any{"delete_local_events": false, "purge_up_to_ts": before.UnixMilli()})
}
