# build.py DBIP_CITY.csv.gz DBIP_COUNTRY.csv.gz NE_COUNTRIES.geojson
#     Writes internal/geo/ip.bin and internal/geo/shapes.go, what the Host
#     screen's swarm map needs, from:
#       DB-IP Lite, City and Country (CC BY 4.0, https://db-ip.com/db/lite.php):
#         Iran's IPv4 and IPv6 ranges to a place, the world's IPv4 ranges to a
#         country, and each country's middle (its rows' mean position);
#       Natural Earth's 1:50m countries (public domain): Iran's border and its
#         neighbours', simplified.
#     Run it from the repository's root; refresh now and then, as IPs move.
import csv, gzip, io, json, math, re, struct, sys, unicodedata
from collections import defaultdict
from ipaddress import ip_address

city_csv, country_csv, ne = sys.argv[1:4]
BOX = (40.0, 22.0, 67.0, 42.0)  # lon/lat the map can show, with room around Iran


def plain(s):
    s = re.sub(r"\s*\(.*?\)", "", s)  # "Tehran (District 6)"
    s = "".join(c for c in unicodedata.normalize("NFD", s) if not unicodedata.combining(c))
    return re.sub(r"\s+Province$", "", s.replace("'", "").replace("`", "")).strip()


def uvarint(n):
    out = bytearray()
    while True:
        b = n & 0x7F
        n >>= 7
        if n:
            out.append(b | 0x80)
        else:
            out.append(b)
            return bytes(out)


def svarint(n):
    return uvarint((n << 1) ^ (n >> 63) if n < 0 else n << 1)


def text(s):
    b = s.encode()
    return uvarint(len(b)) + b


places, place_ids, sums = [], {}, defaultdict(lambda: [0.0, 0.0, 0])
ir4, ir6 = [], []
with gzip.open(city_csv, "rt", encoding="utf-8") as f:
    for row in csv.reader(f):
        start, end, _, cc, prov, city, lat, lon = row
        lat, lon = float(lat), float(lon)
        if cc not in ("ZZ", ""):
            s = sums[cc]
            s[0] += lat; s[1] += lon; s[2] += 1
        if cc != "IR":
            continue
        key = (plain(prov), plain(city) or plain(prov))
        if key not in place_ids:
            place_ids[key] = len(places)
            places.append([key, 0.0, 0.0, 0])
        p = places[place_ids[key]]
        p[1] += lat; p[2] += lon; p[3] += 1
        a, b = ip_address(start), ip_address(end)
        if a.version == 4:
            ir4.append((int(a), int(b), place_ids[key]))
        else:
            ir6.append((int(a) >> 64, int(b) >> 64, place_ids[key]))

countries = sorted(c for c, s in sums.items() if s[2])
cid = {c: i for i, c in enumerate(countries)}
world = []
with gzip.open(country_csv, "rt", encoding="utf-8") as f:
    for start, end, cc in csv.reader(f):
        a = ip_address(start)
        if a.version == 4 and cc in cid:
            world.append((int(a), int(ip_address(end)), cid[cc]))


def ranges(rs):
    """Sorted, adjacent ranges of one value merged, as gap, length, value."""
    rs.sort()
    merged = []
    for a, b, v in rs:
        if merged and merged[-1][2] == v and merged[-1][1] + 1 >= a:
            merged[-1][1] = max(merged[-1][1], b)
        elif merged and a <= merged[-1][1]:
            continue  # overlapping /64s of different places: the first wins
        else:
            merged.append([a, b, v])
    out, last = bytearray(uvarint(len(merged))), -1
    for a, b, v in merged:
        out += uvarint(a - last - 1) + uvarint(b - a) + uvarint(v)
        last = b
    return bytes(out)


blob = bytearray(b"IZG1")
blob += uvarint(len(places))
for (prov, city), lat, lon, n in places:
    blob += svarint(round(lat / n * 100)) + svarint(round(lon / n * 100)) + text(city) + text(prov)
blob += uvarint(len(countries))
for c in countries:
    s = sums[c]
    blob += c.encode() + svarint(round(s[0] / s[2] * 100)) + svarint(round(s[1] / s[2] * 100))
blob += ranges(ir4) + ranges(ir6) + ranges(world)
with open("internal/geo/ip.bin", "wb") as f:
    f.write(gzip.compress(bytes(blob), 9, mtime=0))


def simplify(pts, tol):
    """Douglas-Peucker."""
    if len(pts) < 3:
        return pts
    (x1, y1), (x2, y2) = pts[0], pts[-1]
    dx, dy = x2 - x1, y2 - y1
    norm = math.hypot(dx, dy) or 1e-9
    far, at = 0, 0
    for i in range(1, len(pts) - 1):
        d = abs(dy * (pts[i][0] - x1) - dx * (pts[i][1] - y1)) / norm if (dx or dy) else math.hypot(pts[i][0] - x1, pts[i][1] - y1)
        if d > far:
            far, at = d, i
    if far <= tol:
        return [pts[0], pts[-1]]
    return simplify(pts[: at + 1], tol)[:-1] + simplify(pts[at:], tol)


shapes = []
for feat in json.load(open(ne))["features"]:
    code = feat["properties"]["ISO_A2"]
    g = feat["geometry"]
    polys = g["coordinates"] if g["type"] == "MultiPolygon" else [g["coordinates"]]
    for poly in polys:
        ring = poly[0]
        if not any(BOX[0] <= x <= BOX[2] and BOX[1] <= y <= BOX[3] for x, y in ring):
            continue
        ring = simplify(ring, 0.04 if code == "IR" else 0.08)
        if len(ring) >= 4:
            shapes.append((code, ring))
shapes.sort(key=lambda s: (s[0] != "IR", s[0]))

with open("internal/geo/shapes.go", "w") as f:
    f.write("// Code generated by scripts/geo/build.py from Natural Earth (public domain). DO NOT EDIT.\n\n")
    f.write("package geo\n\n// Shapes are Iran's border and its neighbours', as lon/lat rings; Iran's first.\n")
    f.write("var Shapes = []Shape{\n")
    for code, ring in shapes:
        pts = ", ".join(f"{{{x:.2f}, {y:.2f}}}" for x, y in ring)
        f.write(f'\t{{"{code}", [][2]float64{{{pts}}}}},\n')
    f.write("}\n")
print(f"{len(places)} places, {len(countries)} countries, {len(ir4)}+{len(ir6)} Iranian and {len(world)} world rows, "
      f"{len(shapes)} shapes", file=sys.stderr)
