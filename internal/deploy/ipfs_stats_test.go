package deploy

import "testing"

func TestParseHostStats(t *testing.T) {
	s := parseHostStats(`installed yes
active active
follow activating
result success
next 1793400000
parts images
last fetching bafyoci
free 120000000000
peers 7
repo {"RepoSize":5000,"StorageMax":1}
bw Bandwidth TotalIn: 900 B TotalOut: 1.5 kB RateIn: 12.5 B/s RateOut: 3 MB/s
bitswap {"BlocksReceived":40,"DataReceived":4000,"BlocksSent":2,"DataSent":200}
index { "version": 9, "created": "2026-10-29T23:18:50Z" }
pins bafygit bafyother
links {"Data":{"/":{"bytes":"CAE"}},"Links":[{"Hash":{"/":"bafypkg"},"Name":"packages","Tsize":3000},{"Hash":{"/":"bafyoci"},"Name":"oci","Tsize":10000},{"Hash":{"/":"bafygit"},"Name":"git","Tsize":70},{"Hash":{"/":"bafyidx"},"Name":"index.json","Tsize":9}]}
`)
	if !s.Active || !s.Following || s.Failed || s.Peers != 7 || s.RateIn != 12.5 || s.TotalOut != 1500 || s.RateOut != 3e6 || s.DataSent != 200 || s.Published != "2026-10-29T23:18:50Z" {
		t.Fatalf("%+v", s)
	}
	// Images only: the OCI part is wanted, and what's in the repo counts as arriving.
	if s.Wanted != 10000 || s.Received != 5000 || len(s.PublishedParts) != 3 || !s.PublishedParts[2].Pinned || s.PublishedParts[1].Pinned {
		t.Fatalf("wanted %d received %d parts %v", s.Wanted, s.Received, s.PublishedParts)
	}
	// interzone-mirror's node pins only the root: every part under it is kept.
	m := parseHostStats(`installed yes
mirror interzone-mirror-ipfs-1
active active
parts all
root bafyroot
pins bafyroot
links {"Links":[{"Hash":{"/":"bafypkg"},"Name":"packages","Tsize":3000},{"Hash":{"/":"bafyoci"},"Name":"oci","Tsize":10000}]}
`)
	if m.Mirror != "interzone-mirror-ipfs-1" || len(m.Parts) != 4 || m.Wanted != 13000 || m.Received != 13000 {
		t.Fatalf("%+v", m)
	}
	if s := parseHostStats("installed no\n"); s.Installed || s.Active {
		t.Fatalf("%+v", s)
	}
}
