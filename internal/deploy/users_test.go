package deploy

import "testing"

// The admin script's list: people with what they use, a bridge's bot and
// puppet without, and the server's room count.
func TestParseUsers(t *testing.T) {
	out := `{"users": [
		{"name": "@alice:example.org", "admin": 1, "displayname": "Alice", "creation_ts": 1700000000000, "last_seen_ts": 1761400000000,
		 "avatar_url": "mxc://x", "appservice": "", "bot": false, "rooms": 27, "devices": 2, "threepids": ["alice@example.org"], "media": [142, 398458880]},
		{"name": "@bob:example.org", "admin": false, "deactivated": true, "locked": true, "appservice": "", "rooms": 0, "devices": 0, "media": [0, 0]},
		{"name": "@telegrambot:example.org", "appservice": "telegram", "bot": true, "media": [0, 0]},
		{"name": "@telegram_123:example.org", "appservice": "telegram", "user_type": "bot", "media": [3, 1024]}
	], "rooms": 31, "token": "t"}`
	list, err := parseUsers([]byte(out), map[string]bool{"bob": true})
	if err != nil {
		t.Fatal(err)
	}
	if list.Rooms != 31 || len(list.Users) != 4 {
		t.Fatalf("got %d rooms, %d users", list.Rooms, len(list.Users))
	}
	a, b, bot, puppet := list.Users[0], list.Users[1], list.Users[2], list.Users[3]
	if a.Admin || !b.Admin {
		t.Error("MAS's admins should override Synapse's flag")
	}
	if a.Rooms != 27 || a.Devices != 2 || a.MediaCount != 142 || a.MediaBytes != 398458880 || !a.Avatar || len(a.Emails) != 1 || !a.Person() {
		t.Errorf("alice: %+v", a)
	}
	if !b.Deactivated || !b.Locked {
		t.Errorf("bob: %+v", b)
	}
	if bot.Person() || !bot.Bot || bot.Rooms != -1 || bot.Devices != -1 {
		t.Errorf("the bridge bot: %+v", bot)
	}
	if puppet.Bridge != "telegram" || puppet.Bot || puppet.MediaCount != 3 || puppet.Type != "bot" {
		t.Errorf("the puppet: %+v", puppet)
	}
}
