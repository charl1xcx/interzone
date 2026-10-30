// Package geo places IP addresses on the Host screen's map of Iran, offline:
// Iranian addresses to a town, others to a country. The data is DB-IP Lite
// (https://db-ip.com, CC BY 4.0) and Natural Earth's borders, made by
// scripts/geo/build.py.
package geo

import (
	"bufio"
	"bytes"
	"compress/gzip"
	_ "embed"
	"encoding/binary"
	"io"
	"net/netip"
	"sort"
	"sync"
)

// Credit is the attribution DB-IP's license asks for.
const Credit = "IP locations by DB-IP.com"

// Shape is a country's border ring, lon/lat pairs.
type Shape struct {
	Country string
	Ring    [][2]float64
}

// Place is where an address is.
type Place struct {
	Country  string // ISO 3166 code
	Town     string // in Iran only
	Province string
	Lat, Lon float64
}

//go:embed ip.bin
var raw []byte

type span struct {
	start, end uint64
	v          int
}

type table struct {
	places    []Place
	countries []Place
	ir4, ir6  []span
	world     []span
}

var (
	once sync.Once
	data *table
)

func load() *table {
	once.Do(func() {
		zr, err := gzip.NewReader(bytes.NewReader(raw))
		if err != nil {
			panic(err)
		}
		r := bufio.NewReader(zr)
		var magic [4]byte
		if _, err := io.ReadFull(r, magic[:]); err != nil || string(magic[:]) != "IZG1" {
			panic("geo: bad ip.bin")
		}
		u := func() uint64 { n, _ := binary.ReadUvarint(r); return n }
		s := func() float64 { n, _ := binary.ReadVarint(r); return float64(n) / 100 }
		str := func() string {
			b := make([]byte, u())
			_, _ = io.ReadFull(r, b)
			return string(b)
		}
		t := &table{}
		for n := u(); n > 0; n-- {
			lat, lon := s(), s()
			town := str()
			t.places = append(t.places, Place{Country: "IR", Lat: lat, Lon: lon, Town: town, Province: str()})
		}
		for n := u(); n > 0; n-- {
			var cc [2]byte
			_, _ = io.ReadFull(r, cc[:])
			lat, lon := s(), s()
			t.countries = append(t.countries, Place{Country: string(cc[:]), Lat: lat, Lon: lon})
		}
		spans := func() []span {
			out := make([]span, u())
			last := ^uint64(0) // -1, so the first gap is the first start
			for i := range out {
				a := last + 1 + u()
				b := a + u()
				out[i], last = span{a, b, int(u())}, b
			}
			return out
		}
		t.ir4, t.ir6, t.world = spans(), spans(), spans()
		data = t
	})
	return data
}

func find(spans []span, ip uint64) (int, bool) {
	i := sort.Search(len(spans), func(i int) bool { return spans[i].end >= ip })
	if i < len(spans) && spans[i].start <= ip {
		return spans[i].v, true
	}
	return 0, false
}

// Locate says where an address is; false for private and unknown ones.
func Locate(a netip.Addr) (Place, bool) {
	a = a.Unmap()
	if !a.IsGlobalUnicast() || a.IsPrivate() {
		return Place{}, false
	}
	t := load()
	if a.Is4() {
		ip := uint64(binary.BigEndian.Uint32(a.AsSlice()))
		if i, ok := find(t.ir4, ip); ok {
			return t.places[i], true
		}
		if i, ok := find(t.world, ip); ok {
			return t.countries[i], true
		}
		return Place{}, false
	}
	b := a.As16()
	if i, ok := find(t.ir6, binary.BigEndian.Uint64(b[:8])); ok {
		return t.places[i], true
	}
	return Place{}, false
}
