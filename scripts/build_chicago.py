#!/usr/bin/env python3
"""Build configs/chicago/neighborhoods.json for Divvy (Chicago).

Divvy's ~2,015 stations are almost all in Chicago's 77 official Community Areas, plus a
small Evanston (Northwestern) cluster. Geography:
  - neighborhood = Community Area (e.g. Lincoln Park, Loop, Hyde Park),
  - area = the Community Area's "side" (Chicago's 9 official sides: Far North, North,
    Central, West, South, …) so the Everywhere view rolls up sensibly instead of one blob.
  - Evanston stations → an "Evanston" neighborhood/area (hand-drawn ring; matched only after
    the Community Areas, so the Chicago border is clean).

Same ray-cast as the Go ingester; rings simplified for the ConfigMap limit. Inputs cached in
/tmp (Divvy GBFS station_information + the Chicago Community Areas GeoJSON, Socrata igwz-8jzy).
Side slugs carry "-side" so titlecasing them yields the right display (no AREA_OVERRIDES needed).
Run:  python3 scripts/build_chicago.py
"""
import json
import math
import os
import re
import urllib.parse
import urllib.request
from collections import defaultdict

HERE = os.path.dirname(__file__)
OUTDIR = os.path.join(HERE, "..", "configs", "chicago")
CA = os.environ.get("CHI_CA", "/tmp/chi_ca.geojson")
STATIONS = os.environ.get("CHI_STATIONS", "/tmp/chi_si.json")
_MIN_STEP = 0.0002
_UA = "bikeshare-web/1.0 (kardolus@gmail.com)"

# Community-area number → Chicago "side" (the canonical 9-side grouping).
_SIDES = {
    "far-north-side": [1, 2, 3, 4, 9, 10, 11, 12, 13, 14, 76, 77],
    "northwest-side": [15, 16, 17, 18, 19, 20],
    "north-side": [5, 6, 7, 21, 22],
    "west-side": [23, 24, 25, 26, 27, 28, 29, 30, 31],
    "central": [8, 32, 33],
    "south-side": [34, 35, 36, 37, 38, 39, 40, 41, 42, 43, 60, 69],
    "southwest-side": [56, 57, 58, 59, 61, 62, 63, 64, 65, 66, 67, 68],
    "far-southwest-side": [70, 71, 72, 73, 74, 75],
    "far-southeast-side": [44, 45, 46, 47, 48, 49, 50, 51, 52, 53, 54, 55],
}
_NUM_SIDE = {n: s for s, nums in _SIDES.items() for n in nums}

# Evanston (incl. the Northwestern stations) — its real OSM municipal boundary, fetched
# below. Only matched after the Community Areas, so it never steals Chicago-side stations.
# Fallback box (used only if the OSM fetch fails) so the build stays offline-robust.
_EVANSTON_BOX = [[42.010, -87.650], [42.010, -87.745], [42.080, -87.745], [42.080, -87.650]]


def evanston_rings():
    """Evanston's real city boundary from OSM/Nominatim (the administrative polygon),
    as simplified [lat,lon] rings. Falls back to a bounding box if the fetch fails."""
    cache = "/tmp/osm-evanston.json"
    try:
        if os.path.exists(cache):
            d = json.load(open(cache))
        else:
            url = ("https://nominatim.openstreetmap.org/search?"
                   + urllib.parse.urlencode({"q": "Evanston, Cook County, Illinois",
                                             "polygon_geojson": 1, "format": "json",
                                             "limit": 5, "featuretype": "city"}))
            with urllib.request.urlopen(urllib.request.Request(url, headers={"User-Agent": _UA}), timeout=60) as r:
                d = json.load(r)
            json.dump(d, open(cache, "w"))
        feat = next(x for x in d if x.get("class") == "boundary" and x.get("type") == "administrative")
        return grings(feat["geojson"])
    except Exception as e:  # network/parse failure — keep the build working offline
        print("WARN Evanston OSM boundary unavailable, using fallback box:", e)
        return [_simplify(_EVANSTON_BOX)]


def slugify(s):
    return re.sub(r"[^a-z0-9]+", "-", s.lower()).strip("-")


def titlecase(s):
    return " ".join(w.capitalize() for w in s.split())


def ray_inside(lat, lon, ring):
    inside, n, j = False, len(ring), len(ring) - 1
    for i in range(n):
        yi, xi = ring[i]
        yj, xj = ring[j]
        if ((yi > lat) != (yj > lat)) and (lon < (xj - xi) * (lat - yi) / (yj - yi) + xi):
            inside = not inside
        j = i
    return inside


def _simplify(ring):
    out = []
    for lat, lon in ring:
        lat, lon = round(lat, 5), round(lon, 5)
        if not out or abs(lat - out[-1][0]) + abs(lon - out[-1][1]) >= _MIN_STEP:
            out.append([lat, lon])
    if len(out) >= 3 and out[0] != out[-1]:
        out.append(out[0])
    return out if len(out) >= 4 else [[round(la, 5), round(lo, 5)] for la, lo in ring]


def grings(g):
    polys = g["coordinates"] if g["type"] == "MultiPolygon" else [g["coordinates"]]
    return [_simplify([[lat, lon] for lon, lat in poly[0]]) for poly in polys]


def load_sources():
    out = []
    for f in json.load(open(CA))["features"]:
        p = f["properties"]
        num = int(p["area_numbe"])
        side = _NUM_SIDE.get(num, "chicago")
        out.append((slugify(p["community"]), titlecase(p["community"]), side, grings(f["geometry"])))
    out.append(("evanston", "Evanston", "evanston", evanston_rings()))
    return out


def main():
    sources = load_sources()
    print(f"loaded {len(sources)} candidates ({len(sources)-1} community areas + Evanston)")
    stations = [s for s in json.load(open(STATIONS))["data"]["stations"]
                if s.get("lat") is not None and s.get("lon") is not None]
    members, meta, leftover = defaultdict(list), {}, []
    for st in stations:
        lat, lon = st["lat"], st["lon"]
        hit = next((s for s in sources if any(ray_inside(lat, lon, r) for r in s[3])), None)
        if hit is None:
            leftover.append(st); continue
        members[hit[0]].append((lat, lon)); meta[hit[0]] = hit[1:]
    cent = {s: (sum(p[0] for p in m) / len(m), sum(p[1] for p in m) / len(m)) for s, m in members.items()}
    src = {s[0]: s for s in sources}

    def nearest(lat, lon):
        best, bd = None, 1e18
        for s, (cl, co) in cent.items():
            d = (lat - cl) ** 2 + ((lon - co) * math.cos(math.radians(lat))) ** 2
            if d < bd:
                bd, best = d, s
        return best
    snapped = 0
    for st in leftover:
        s = nearest(st["lat"], st["lon"])
        if s:
            members[s].append((st["lat"], st["lon"])); meta[s] = src[s][1:]; snapped += 1
    order = {s[0]: i for i, s in enumerate(sources)}
    out = []
    for slug in sorted(members, key=lambda s: order[s]):
        disp, area, rings = meta[slug]
        pts = members[slug]
        out.append({"slug": slug, "display": disp, "area": area,
                    "centroid": [round(sum(p[0] for p in pts) / len(pts), 5),
                                 round(sum(p[1] for p in pts) / len(pts), 5)],
                    "count": len(pts), "rings": rings})
    os.makedirs(OUTDIR, exist_ok=True)
    json.dump(out, open(os.path.join(OUTDIR, "neighborhoods.json"), "w"))
    json.dump([{k: n[k] for k in ("slug", "display", "area", "centroid", "count")} for n in out],
              open(os.path.join(OUTDIR, "neighborhoods.meta.json"), "w"), indent=0)
    by_area = defaultdict(lambda: [0, 0])
    for n in out:
        by_area[n["area"]][0] += 1
        by_area[n["area"]][1] += n["count"]
    print(f"\nwrote {len(out)} neighborhoods covering {sum(n['count'] for n in out)}/{len(stations)} "
          f"stations ({snapped} snapped):")
    for a in sorted(by_area, key=lambda a: -by_area[a][1]):
        nh, ns = by_area[a]
        print(f"  {a:20s} {nh:3d} nbhds  {ns:4d} stations")


if __name__ == "__main__":
    main()
