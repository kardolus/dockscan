#!/usr/bin/env python3
"""Build client/neighborhoods.json for the city-wide expansion.

Sources, in first-match precedence order (a station is assigned to the first
neighborhood whose polygon contains it):
  1. the 29 hand-curated Brooklyn polygons (kept pristine — area=brooklyn),
  2. hand-drawn Jersey City + Hoboken polygons,
  3. NYC 2020 NTAs (residential, ntatype='0') for Manhattan/Queens/Bronx/Staten Island.

The point-in-polygon here is the SAME ray-cast the Go ingester uses (multi-ring,
holes ignored), so the offline assignment matches runtime exactly. We emit only
neighborhoods that actually have Citi Bike stations, each with its `area`, `rings`
([lat,lon] outer rings), a `centroid` (mean of member stations) and `count`, for
the web's area roll-ups and the citywide map bubbles.

Run:  python3 scripts/build_neighborhoods.py
"""
import json
import os
import re
import sys
import urllib.request
from collections import defaultdict

HERE = os.path.dirname(__file__)
REPO = os.path.join(HERE, "..")
OUT = os.path.join(REPO, "client", "neighborhoods.json")
NTA_URL = "https://data.cityofnewyork.us/api/geospatial/9nt8-h7nd?method=export&format=GeoJSON"
GBFS = "https://gbfs.citibikenyc.com/gbfs/en/station_information.json"
NTA_CACHE = os.environ.get("NTA_CACHE", "/tmp/nta2020.geojson")

BORO_AREA = {"Manhattan": "manhattan", "Queens": "queens", "Bronx": "bronx",
             "Staten Island": "staten-island"}  # Brooklyn handled by the curated set


def slugify(s):
    s = re.sub(r"[^a-z0-9]+", "-", s.lower()).strip("-")
    return s


def ray_inside(lat, lon, ring):
    """Ray-casting point-in-ring on a ring of [lat, lon] points (matches the Go code)."""
    inside = False
    n = len(ring)
    j = n - 1
    for i in range(n):
        yi, xi = ring[i]
        yj, xj = ring[j]
        if ((yi > lat) != (yj > lat)) and (lon < (xj - xi) * (lat - yi) / (yj - yi) + xi):
            inside = not inside
        j = i
    return inside


def contains(rings, lat, lon):
    return any(ray_inside(lat, lon, r) for r in rings)


def fetch_json(url, cache=None):
    if cache and os.path.exists(cache):
        return json.load(open(cache))
    with urllib.request.urlopen(url, timeout=60) as r:
        d = json.load(r)
    if cache:
        json.dump(d, open(cache, "w"))
    return d


# --- hand-drawn Jersey City + Hoboken (rough rings of [lat, lon], CCW). Tuned to the
#     Citi Bike footprint; refine boxes from the coverage report. ------------------------
JC_HOBOKEN = [
    ("hoboken", "Hoboken", "hoboken",
     [[40.7355, -74.0300], [40.7355, -74.0405], [40.7570, -74.0405], [40.7570, -74.0235], [40.7440, -74.0235], [40.7440, -74.0300]]),
    ("jc-downtown", "Downtown Jersey City", "jersey-city",
     [[40.7050, -74.0300], [40.7050, -74.0520], [40.7270, -74.0520], [40.7270, -74.0300]]),
    ("jc-the-heights", "The Heights", "jersey-city",
     [[40.7400, -74.0400], [40.7400, -74.0640], [40.7620, -74.0640], [40.7620, -74.0400]]),
    ("jc-journal-square", "Journal Square", "jersey-city",
     [[40.7180, -74.0520], [40.7180, -74.0760], [40.7400, -74.0760], [40.7400, -74.0520]]),
    ("jc-bergen-lafayette", "Bergen-Lafayette", "jersey-city",
     [[40.6960, -74.0560], [40.6960, -74.0820], [40.7180, -74.0820], [40.7180, -74.0560]]),
    ("jc-west-side", "West Side", "jersey-city",
     [[40.7100, -74.0760], [40.7100, -74.0960], [40.7400, -74.0960], [40.7400, -74.0760]]),
    ("jc-greenville", "Greenville", "jersey-city",
     [[40.6840, -74.0640], [40.6840, -74.0920], [40.6960, -74.0920], [40.6960, -74.0640]]),
]


def load_sources():
    """Returns an ordered list of (slug, display, area, rings) in precedence order."""
    out = []
    # 1. curated Brooklyn (pristine) — always from the committed snapshot, never from OUT
    cur = json.load(open(os.path.join(HERE, "neighborhoods.brooklyn.json")))
    for n in cur:
        rings = n.get("rings") or [n["polygon"]]   # real polygons (rings) or legacy single box
        out.append((n["slug"], n["display"], "brooklyn", rings))
    # 2. JC + Hoboken
    for slug, disp, area, ring in JC_HOBOKEN:
        out.append((slug, disp, area, [ring]))
    # 3. NYC NTAs (residential), non-Brooklyn
    nta = fetch_json(NTA_URL, NTA_CACHE)
    for f in nta["features"]:
        p = f["properties"]
        if p.get("ntatype") != "0":
            continue
        area = BORO_AREA.get(p.get("boroname"))
        if not area:  # skip Brooklyn (curated) and anything unmapped
            continue
        rings = []
        for poly in f["geometry"]["coordinates"]:  # MultiPolygon
            outer = poly[0]  # outer ring; holes (poly[1:]) ignored
            rings.append([[lat, lon] for lon, lat in outer])
        out.append((slugify(p["ntaname"]), p["ntaname"], area, rings))
    return out


def main():
    sources = load_sources()
    print(f"loaded {len(sources)} candidate neighborhoods "
          f"({sum(1 for s in sources if s[2]=='brooklyn')} curated Brooklyn, "
          f"{len(JC_HOBOKEN)} JC/Hoboken, rest NTAs)")

    stations = fetch_json(GBFS)["data"]["stations"]
    stations = [s for s in stations if s.get("lat") is not None and s.get("lon") is not None]
    members = defaultdict(list)   # slug -> [(lat,lon)]
    meta = {}                      # slug -> (display, area, rings)
    leftover = []                  # stations not inside any polygon

    # Pass 1: polygon first-match.
    for st in stations:
        lat, lon = st["lat"], st["lon"]
        hit = None
        for slug, disp, area, rings in sources:
            if contains(rings, lat, lon):
                hit = (slug, disp, area, rings)
                break
        if hit is None:
            leftover.append(st)
            continue
        slug, disp, area, rings = hit
        members[slug].append((lat, lon))
        meta[slug] = (disp, area, rings)

    # Provisional centroids from polygon-assigned members, for the nearest-centroid fallback.
    cent = {s: (sum(p[0] for p in m) / len(m), sum(p[1] for p in m) / len(m))
            for s, m in members.items()}
    src_by_slug = {s[0]: s for s in sources}

    def nearest(lat, lon):
        import math
        best, bd = None, 1e18
        for s, (clat, clon) in cent.items():
            d = (lat - clat) ** 2 + ((lon - clon) * math.cos(math.radians(lat))) ** 2
            if d < bd:
                bd, best = d, s
        return best

    # Pass 2: snap leftovers to the nearest existing neighborhood centroid.
    snapped = 0
    for st in leftover:
        slug = nearest(st["lat"], st["lon"])
        if slug is None:
            continue
        _, disp, area, rings = src_by_slug[slug]
        members[slug].append((st["lat"], st["lon"]))
        meta[slug] = (disp, area, rings)
        snapped += 1
    unmapped = len(leftover) - snapped

    # emit only neighborhoods with >=1 station; curated Brooklyn first, then by area+display
    order = {s[0]: i for i, s in enumerate(sources)}
    out = []
    for slug in sorted(members, key=lambda s: order[s]):
        disp, area, rings = meta[slug]
        pts = members[slug]
        clat = round(sum(p[0] for p in pts) / len(pts), 5)
        clon = round(sum(p[1] for p in pts) / len(pts), 5)
        out.append({"slug": slug, "display": disp, "area": area,
                    "centroid": [clat, clon], "count": len(pts), "rings": rings})

    json.dump(out, open(OUT, "w"))
    # Lightweight meta (no rings) for the web app's registry + map bubbles.
    meta_out = os.path.join(HERE, "neighborhoods.meta.json")
    json.dump([{k: n[k] for k in ("slug", "display", "area", "centroid", "count")} for n in out],
              open(meta_out, "w"), indent=0)
    print(f"wrote web meta → {os.path.normpath(meta_out)} ({os.path.getsize(meta_out)//1024} KB)")
    # report
    by_area = defaultdict(lambda: [0, 0])
    for n in out:
        by_area[n["area"]][0] += 1
        by_area[n["area"]][1] += n["count"]
    print(f"\nwrote {len(out)} neighborhoods covering {sum(n['count'] for n in out)} stations "
          f"({snapped} snapped to nearest, {unmapped} still unmapped of {len(stations)}):")
    for area in sorted(by_area):
        n_hoods, n_st = by_area[area]
        print(f"  {area:14s} {n_hoods:3d} neighborhoods  {n_st:4d} stations")


if __name__ == "__main__":
    main()
