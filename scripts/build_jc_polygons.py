#!/usr/bin/env python3
"""Regenerate scripts/neighborhoods.jc.json with real polygons (was hand-drawn boxes).

Jersey City + Hoboken sat outside the NYC NTA dataset, so build_neighborhoods.py used
crude axis-aligned bounding boxes for them — they looked blocky on the map next to the
NTA-derived NYC shapes. NJ has no NTA equivalent, so instead:

  * Hoboken is one small municipality -> use its real OSM administrative boundary directly.
  * Jersey City's 6 curated neighborhoods (Downtown, The Heights, Journal Square,
    Bergen-Lafayette, West Side, Greenville) are split by a Voronoi partition seeded on each
    neighborhood's *current* live-station centroid, clipped to Jersey City's real OSM
    municipal boundary — same treatment as the merged Brooklyn NTAs, so every station stays
    in the neighborhood it's in today with clean, city-clipped, non-overlapping boundaries.

Boundaries come from OSM/Nominatim (administrative polygons); cached under /tmp. Output rings
are [lat,lon] outer rings (multi-ring supported). Run:  python3 scripts/build_jc_polygons.py
"""
import json, os, time, urllib.parse, urllib.request
from collections import defaultdict
from shapely.geometry import shape, Polygon, MultiPolygon, MultiPoint, Point
from shapely.ops import unary_union, voronoi_diagram
from shapely.validation import explain_validity

HERE = os.path.dirname(__file__)
OUT = os.path.join(HERE, "neighborhoods.jc.json")
GBFS = "https://gbfs.citibikenyc.com/gbfs/en/station_information.json"
NOMINATIM = "https://nominatim.openstreetmap.org/search"
UA = "bikeshare-web/1.0 (kardolus@gmail.com)"
SIMPLIFY = 0.00012   # ~12m; trims OSM vertex counts without visibly moving the boundary

# The 6 Jersey City neighborhoods, in emit order. The boxes are ONLY used to seed the
# Voronoi split from current station positions (kept identical to the retired JC_HOBOKEN
# boxes so no station changes neighborhood); the emitted shape is the clipped Voronoi cell.
JC_SEED_BOXES = [
    ("jc-downtown", "Downtown Jersey City",
     [[40.7050, -74.0300], [40.7050, -74.0520], [40.7270, -74.0520], [40.7270, -74.0300]]),
    ("jc-the-heights", "The Heights",
     [[40.7400, -74.0400], [40.7400, -74.0640], [40.7620, -74.0640], [40.7620, -74.0400]]),
    ("jc-journal-square", "Journal Square",
     [[40.7180, -74.0520], [40.7180, -74.0760], [40.7400, -74.0760], [40.7400, -74.0520]]),
    ("jc-bergen-lafayette", "Bergen-Lafayette",
     [[40.6960, -74.0560], [40.6960, -74.0820], [40.7180, -74.0820], [40.7180, -74.0560]]),
    ("jc-west-side", "West Side",
     [[40.7100, -74.0760], [40.7100, -74.0960], [40.7400, -74.0960], [40.7400, -74.0760]]),
    ("jc-greenville", "Greenville",
     [[40.6840, -74.0640], [40.6840, -74.0920], [40.6960, -74.0920], [40.6960, -74.0640]]),
]


def fetch_json(url, cache=None, headers=None):
    if cache and os.path.exists(cache):
        return json.load(open(cache))
    req = urllib.request.Request(url, headers=headers or {})
    with urllib.request.urlopen(req, timeout=60) as r:
        d = json.load(r)
    if cache:
        json.dump(d, open(cache, "w"))
    return d


def osm_boundary(query):
    """Fetch a municipality's administrative boundary from Nominatim as a shapely (lon,lat) geom."""
    cache = "/tmp/osm-" + urllib.parse.quote(query, safe="") + ".json"
    url = f"{NOMINATIM}?q={urllib.parse.quote(query)}&polygon_geojson=1&format=json&limit=1"
    d = fetch_json(url, cache, headers={"User-Agent": UA})
    if not d:
        raise SystemExit(f"no OSM result for {query!r}")
    return shape(d[0]["geojson"])


def ray(lat, lon, ring):
    inside = False; n = len(ring); j = n - 1
    for i in range(n):
        yi, xi = ring[i]; yj, xj = ring[j]
        if ((yi > lat) != (yj > lat)) and (lon < (xj - xi) * (lat - yi) / (yj - yi) + xi):
            inside = not inside
        j = i
    return inside


def to_rings(geom):
    """shapely (lon,lat) geometry -> list of [lat,lon] outer rings (largest first)."""
    geom = geom.buffer(0)
    polys = list(geom.geoms) if isinstance(geom, MultiPolygon) else [geom]
    polys = [p for p in polys if p.area > 1e-7]
    polys.sort(key=lambda p: -p.area)
    out = []
    for p in polys:
        ring = [[round(y, 6), round(x, 6)] for x, y in p.exterior.coords]
        rp = Polygon([(lon, lat) for lat, lon in ring]).buffer(0)
        for q in (list(rp.geoms) if isinstance(rp, MultiPolygon) else [rp]):
            if q.area > 1e-7:
                out.append([[round(y, 6), round(x, 6)] for x, y in q.exterior.coords])
    return out


def main():
    jc = osm_boundary("Jersey City, New Jersey")
    time.sleep(1)   # be polite to Nominatim
    hob = osm_boundary("Hoboken, New Jersey")
    print(f"OSM boundaries: Jersey City area={jc.area:.5f}  Hoboken area={hob.area:.5f}")

    stations = fetch_json(GBFS)["data"]["stations"]

    # Seed the Voronoi split with each JC neighborhood's current in-box station centroid.
    pts = defaultdict(list)
    for st in stations:
        lat, lon = st.get("lat"), st.get("lon")
        if lat is None or not jc.contains(Point(lon, lat)):
            continue                       # only stations actually inside Jersey City
        for slug, _disp, ring in JC_SEED_BOXES:
            if ray(lat, lon, ring):
                pts[slug].append((lat, lon)); break
    seeds = {s: Point(sum(p[1] for p in v) / len(v), sum(p[0] for p in v) / len(v))
             for s, v in pts.items() if v}
    missing = [s for s, _, _ in JC_SEED_BOXES if s not in seeds]
    if missing:
        print("WARN no seed stations for:", missing)

    # Voronoi-partition the JC boundary among the seeds, clip each cell to the city outline.
    vor = voronoi_diagram(MultiPoint(list(seeds.values())), envelope=jc.envelope)
    disp_of = {s: d for s, d, _ in JC_SEED_BOXES}
    cells = {}
    for cell in vor.geoms:
        owner = next((s for s, pt in seeds.items() if cell.contains(pt)), None)
        if owner is None:
            continue
        piece = cell.intersection(jc)
        if not piece.is_empty:
            cells[owner] = unary_union([cells[owner], piece]) if owner in cells else piece

    out = []
    for slug, disp, _ in JC_SEED_BOXES:
        if slug not in cells:
            continue
        geom = cells[slug].buffer(0).simplify(SIMPLIFY, preserve_topology=True).buffer(0)
        if geom.is_empty or not geom.is_valid:
            print("WARN geometry issue for", slug, explain_validity(geom))
        out.append({"slug": slug, "display": disp, "area": "jersey-city", "rings": to_rings(geom)})

    hgeom = hob.buffer(0).simplify(SIMPLIFY, preserve_topology=True).buffer(0)
    out.append({"slug": "hoboken", "display": "Hoboken", "area": "hoboken", "rings": to_rings(hgeom)})

    json.dump(out, open(OUT, "w"), indent=1)
    npts = sum(len(r) for n in out for r in n["rings"])
    print(f"wrote {len(out)} JC/Hoboken neighborhoods, {npts} total points -> {os.path.relpath(OUT)}")
    for n in out:
        print(f"   {n['slug']:<22} {sum(len(r) for r in n['rings'])} pts, {len(n['rings'])} ring(s)")


if __name__ == "__main__":
    main()
