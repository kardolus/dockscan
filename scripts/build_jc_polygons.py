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
from shapely.geometry import shape, box, LineString, Polygon, MultiPolygon, MultiPoint, Point
from shapely.ops import unary_union, voronoi_diagram, polygonize
from shapely.validation import explain_validity

HERE = os.path.dirname(__file__)
OUT = os.path.join(HERE, "neighborhoods.jc.json")
GBFS = "https://gbfs.citibikenyc.com/gbfs/en/station_information.json"
NOMINATIM = "https://nominatim.openstreetmap.org/search"
UA = "bikeshare-web/1.0 (kardolus@gmail.com)"
SIMPLIFY = 0.00012   # ~12m; trims OSM vertex counts without visibly moving the boundary

# The NY/NJ state line runs down the middle of the Hudson, so the OSM municipal boundaries
# for Jersey City/Hoboken extend into the river — the Voronoi cells then spill into open
# water. We clip them to actual land using the OSM coastline (the harbor + tidal Hudson are
# coastline-mapped). Bbox covers JC/Hoboken/Bayonne + the surrounding water.
COAST_BBOX = (40.63, -74.18, 40.84, -73.95)   # (S, W, N, E)
OVERPASS = ("https://overpass-api.de/api/interpreter",
            "https://overpass.kumi.systems/api/interpreter")

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


def _overpass(query, cache):
    if os.path.exists(cache):
        return json.load(open(cache))
    body = urllib.parse.urlencode({"data": query}).encode()
    last = None
    for ep in OVERPASS:
        try:
            with urllib.request.urlopen(urllib.request.Request(ep, data=body, headers={"User-Agent": UA}), timeout=120) as r:
                d = json.load(r)
            json.dump(d, open(cache, "w"))
            return d
        except Exception as e:  # try the next mirror
            last = e
    raise SystemExit(f"Overpass unavailable: {last}")


def land_polygon(seed_pts):
    """A land mask for the JC/Hoboken area built from the OSM coastline: polygonize the
    coastline (clipped to COAST_BBOX and closed with the bbox edge) into land/water faces,
    then keep the faces that contain a known-land seed point. Returns a shapely (lon,lat) geom.
    seed_pts are (lat, lon) points known to be on land (e.g. station positions)."""
    S, W, N, E = COAST_BBOX
    q = (f"[out:json][timeout:120];"
         f'(way["natural"="coastline"]({S},{W},{N},{E}););out geom;')
    ways = [w for w in _overpass(q, "/tmp/coastline-nynj.json")["elements"] if w.get("geometry")]
    B = box(W, S, E, N)
    lines = []
    for w in ways:
        c = [(p["lon"], p["lat"]) for p in w["geometry"]]
        if len(c) >= 2:
            x = LineString(c).intersection(B)
            if not x.is_empty:
                lines += list(x.geoms) if x.geom_type == "MultiLineString" else [x]
    tiles = list(polygonize(unary_union(lines + [B.boundary])))
    land = unary_union([t for t in tiles
                        if any(t.contains(Point(lo, la)) for la, lo in seed_pts)]).buffer(0)
    if land.is_empty:
        raise SystemExit("land_polygon: no land face matched the seed points")
    return land


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

    # The NY/NJ line runs down the middle of the Hudson, so the OSM municipal boundaries spill
    # into open water. Clip both to actual land (OSM coastline) so no neighborhood sits in the
    # river. Land seeds = stations inside the raw boundaries (all on land) to pick the NJ face.
    land_seeds = [(st["lat"], st["lon"]) for st in stations if st.get("lat") is not None
                  and (jc.contains(Point(st["lon"], st["lat"])) or hob.contains(Point(st["lon"], st["lat"])))]
    land = land_polygon(land_seeds)
    jc = jc.intersection(land).buffer(0)
    hob = hob.intersection(land).buffer(0)
    print(f"clipped to land:    Jersey City area={jc.area:.5f}  Hoboken area={hob.area:.5f}")

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
