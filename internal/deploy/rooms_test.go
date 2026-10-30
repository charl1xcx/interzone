package deploy

import "testing"

func TestParseRooms(t *testing.T) {
	out := `{"rooms": [
		{"room_id": "!a:x.org", "name": "Lounge", "canonical_alias": "#lounge:x.org", "joined_members": 12, "joined_local_members": 3,
		 "state_events": 80, "public": true, "encryption": null, "federatable": true, "blocked": true,
		 "created": 1700000000000, "media": [172, 24100000]},
		{"room_id": "!b:x.org", "name": null, "canonical_alias": null, "joined_members": 2, "encryption": "m.megolm.v1.aes-sha2"}
	], "token": "t"}`
	rooms, err := parseRooms([]byte(out))
	if err != nil || len(rooms) != 2 {
		t.Fatal(rooms, err)
	}
	if r := rooms[0]; r.Title() != "Lounge" || r.Members != 12 || r.Local != 3 || !r.Blocked || r.Encrypted || !r.Public {
		t.Errorf("%+v", r)
	}
	if r := rooms[0]; r.Created.UnixMilli() != 1700000000000 || r.MediaCount != 172 || r.MediaBytes != 24100000 {
		t.Errorf("dates and media: %+v", r)
	}
	if r := rooms[1]; r.Title() != "!b:x.org" || !r.Encrypted || r.MediaCount != -1 || !r.Created.IsZero() {
		t.Errorf("%+v", r)
	}
}
