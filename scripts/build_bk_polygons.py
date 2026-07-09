#!/usr/bin/env python3
"""Regenerate scripts/neighborhoods.brooklyn.json with real polygons (was hand-drawn boxes).

The 29 curated Brooklyn neighborhoods were originally quick axis-aligned bounding boxes,
which overlapped and looked crude on the map next to the NTA-derived shapes used for the
other boroughs. This derives proper polygons from the NYC 2020 NTAs (the same source
build_neighborhoods.py uses), keeping the curated granularity:

  * ~20 neighborhoods map to a single NTA or a clean union of NTAs (objective).
  * 3 NTAs each merge several curated neighborhoods (Carroll Gardens/Cobble Hill/Gowanus/
    Red Hook; Downtown/DUMBO/Boerum Hill; Windsor Terrace/Greenwood). Those are split by a
    Voronoi partition seeded on each neighborhood's *current* live-station centroid, clipped
    to the official NTA — so every station stays in the neighborhood it's in today, with
    clean non-overlapping boundaries.

Output rings are [lat,lon] outer rings (multi-ring supported). Run:
  python3 scripts/build_bk_polygons.py
"""
import json, os, sys, urllib.request
from collections import defaultdict
from shapely.geometry import shape, Polygon, MultiPolygon, MultiPoint, Point, mapping
from shapely.ops import unary_union, voronoi_diagram
from shapely.validation import explain_validity

HERE = os.path.dirname(__file__)
OUT = os.path.join(HERE, "neighborhoods.brooklyn.json")
NTA_URL = "https://data.cityofnewyork.us/api/geospatial/9nt8-h7nd?method=export&format=GeoJSON"
NTA_CACHE = os.environ.get("NTA_CACHE", "/tmp/nta2020.geojson")
GBFS = "https://gbfs.citibikenyc.com/gbfs/en/station_information.json"
SIMPLIFY = 0.00022   # ~20m; trims NTA vertex counts without visibly moving the boundary

# curated slug -> (display, [NTA names to union])
UNION = {
    "brooklyn-heights": ("Brooklyn Heights", ["Brooklyn Heights"]),
    "greenpoint": ("Greenpoint", ["Greenpoint"]),
    "williamsburg": ("Williamsburg", ["Williamsburg", "South Williamsburg", "East Williamsburg"]),
    "fort-greene": ("Fort Greene", ["Fort Greene"]),
    "clinton-hill": ("Clinton Hill", ["Clinton Hill"]),
    "prospect-heights": ("Prospect Heights", ["Prospect Heights"]),
    "crown-heights": ("Crown Heights", ["Crown Heights (North)", "Crown Heights (South)"]),
    "bedford-stuyvesant": ("Bedford-Stuyvesant", ["Bedford-Stuyvesant (East)", "Bedford-Stuyvesant (West)", "Ocean Hill"]),
    "bushwick": ("Bushwick", ["Bushwick (East)", "Bushwick (West)"]),
    "east-new-york": ("East New York", ["East New York (North)", "East New York-City Line",
                                        "East New York-New Lots", "Cypress Hills"]),
    "brownsville": ("Brownsville", ["Brownsville"]),
    "prospect-lefferts-gardens": ("Prospect Lefferts Gardens", ["Prospect Lefferts Gardens-Wingate"]),
    "east-flatbush": ("East Flatbush", ["East Flatbush-Farragut", "East Flatbush-Rugby",
                                        "East Flatbush-Remsen Village", "East Flatbush-Erasmus"]),
    "flatbush": ("Flatbush", ["Flatbush", "Flatbush (West)-Ditmas Park-Parkville"]),
    "kensington": ("Kensington", ["Kensington"]),
    "borough-park": ("Borough Park", ["Borough Park"]),
    "sunset-park": ("Sunset Park", ["Sunset Park (West)", "Sunset Park (Central)",
                                    "Sunset Park (East)-Borough Park (West)"]),
    "dyker-heights": ("Dyker Heights", ["Dyker Heights"]),
    "bay-ridge": ("Bay Ridge", ["Bay Ridge"]),
}
# merged NTA -> {curated slug: display} members to split by Voronoi(station centroids)
SPLIT = {
    "Carroll Gardens-Cobble Hill-Gowanus-Red Hook": {
        "red-hook": "Red Hook", "carroll-gardens": "Carroll Gardens",
        "cobble-hill": "Cobble Hill", "gowanus": "Gowanus"},
    "Downtown Brooklyn-DUMBO-Boerum Hill": {
        "downtown-brooklyn": "Downtown Brooklyn", "dumbo-vinegar-hill": "DUMBO / Vinegar Hill",
        "boerum-hill": "Boerum Hill"},
    "Windsor Terrace-South Slope": {
        "windsor-terrace": "Windsor Terrace", "greenwood-heights": "Greenwood Heights"},
    # Gowanus straddles two NTAs: its eastern half (around the canal / 4th Ave) is in the
    # Park Slope NTA, so split that too, letting gowanus claim the canal-side strip.
    "Park Slope": {"park-slope": "Park Slope", "gowanus": "Gowanus"},
}


def fetch_json(url, cache=None):
    if cache and os.path.exists(cache):
        return json.load(open(cache))
    with urllib.request.urlopen(url, timeout=60) as r:
        d = json.load(r)
    if cache:
        json.dump(d, open(cache, "w"))
    return d


def to_rings(geom):
    """shapely (lon,lat) geometry -> list of [lat,lon] outer rings (largest first).
    Rounds to 6dp (5dp can collapse vertices into self-intersections) and re-repairs."""
    geom = geom.buffer(0)
    polys = list(geom.geoms) if isinstance(geom, MultiPolygon) else [geom]
    polys = [p for p in polys if p.area > 1e-7]                    # drop slivers (~<1000 m²)
    polys.sort(key=lambda p: -p.area)
    out = []
    for p in polys:
        ring = [[round(y, 6), round(x, 6)] for x, y in p.exterior.coords]
        rp = Polygon([(lon, lat) for lat, lon in ring]).buffer(0)  # repair post-rounding
        for q in (list(rp.geoms) if isinstance(rp, MultiPolygon) else [rp]):
            if q.area > 1e-7:
                out.append([[round(y, 6), round(x, 6)] for x, y in q.exterior.coords])
    return out


def main():
    nta = fetch_json(NTA_URL, NTA_CACHE)
    bk = {}
    for f in nta["features"]:
        p = f["properties"]
        if p.get("ntatype") == "0" and p.get("boroname") == "Brooklyn":
            bk[p["ntaname"]] = shape(f["geometry"])

    # current-box station centroids, to seed the Voronoi splits (keeps stations put)
    boxes = json.load(open(OUT))
    def ray(lat, lon, ring):
        inside = False; n = len(ring); j = n - 1
        for i in range(n):
            yi, xi = ring[i]; yj, xj = ring[j]
            if ((yi > lat) != (yj > lat)) and (lon < (xj - xi) * (lat - yi) / (yj - yi) + xi):
                inside = not inside
            j = i
        return inside
    stations = fetch_json(GBFS)["data"]["stations"]
    pts = defaultdict(list)
    for st in stations:
        lat, lon = st.get("lat"), st.get("lon")
        if lat is None:
            continue
        for b in boxes:
            rings = b.get("rings") or [b["polygon"]]   # tolerate old (box) or new (rings) source
            if any(ray(lat, lon, r) for r in rings):
                pts[b["slug"]].append((lat, lon)); break
    seed = {s: Point(sum(p[1] for p in v) / len(v), sum(p[0] for p in v) / len(v))
            for s, v in pts.items()}                              # (lon,lat)

    pieces = defaultdict(list)   # slug -> [shapely geoms] (a hood = union of its NTAs + split pieces)
    disp_of = {}
    for slug, (disp, names) in UNION.items():
        miss = [n for n in names if n not in bk]
        if miss:
            print("WARN missing NTA(s) for", slug, miss)
        disp_of[slug] = disp
        pieces[slug].append(unary_union([bk[n] for n in names if n in bk]))

    for nta_name, members in SPLIT.items():
        poly = bk[nta_name]
        seeds = {s: seed[s] for s in members if s in seed}
        if len(seeds) < 2:
            print("WARN too few seeds to split", nta_name, list(seeds)); continue
        vor = voronoi_diagram(MultiPoint(list(seeds.values())), envelope=poly.envelope)
        for cell in vor.geoms:
            owner = next((s for s, pt in seeds.items() if cell.contains(pt)), None)
            if owner is None:
                continue
            piece = cell.intersection(poly)
            if not piece.is_empty:
                pieces[owner].append(piece)
        for s, disp in members.items():
            disp_of.setdefault(s, disp)

    order = list(dict.fromkeys(list(UNION) + [s for m in SPLIT.values() for s in m]))
    prelim = {s: unary_union(pieces[s]).buffer(0) for s in order}

    # Preserve coverage: the tight NTA polygons miss ~8% of stations the oversized boxes had
    # (park/industrial edges — Prospect Park perimeter, Green-Wood, the waterfront). Snap each
    # such station into its NEAREST neighborhood, clipped to that neighborhood's Voronoi cell so
    # the patches never overlap. Seeds are centroids of stations actually inside each NTA polygon
    # (so 0-station hoods like dyker-heights/east-new-york get no seed and don't recapture).
    seeds2 = {}
    for s in order:
        m = [(st["lat"], st["lon"]) for st in stations
             if st.get("lat") is not None and prelim[s].contains(Point(st["lon"], st["lat"]))]
        if m:
            seeds2[s] = Point(sum(p[1] for p in m) / len(m), sum(p[0] for p in m) / len(m))
    env = unary_union(list(prelim.values())).buffer(0.02).envelope
    cell_of = {}
    for cell in voronoi_diagram(MultiPoint(list(seeds2.values())), envelope=env).geoms:
        own = next((s for s, pt in seeds2.items() if cell.contains(pt)), None)
        if own:
            cell_of[own] = cell
    allgeom = unary_union(list(prelim.values()))
    patch = defaultdict(list)
    for st in stations:
        lat, lon = st.get("lat"), st.get("lon")
        if lat is None:
            continue
        p = Point(lon, lat)
        if allgeom.contains(p):
            continue
        # only stations the boxes considered Brooklyn (don't pull in other-borough strays)
        if not any(any(ray(lat, lon, r) for r in (b.get("rings") or [b["polygon"]])) for b in boxes):
            continue
        own = min(seeds2, key=lambda s: (p.x - seeds2[s].x) ** 2 + (p.y - seeds2[s].y) ** 2)
        patch[own].append(p)

    out = []
    for slug in order:
        geom = prelim[slug]
        if patch.get(slug):
            pat = MultiPoint(patch[slug]).buffer(0.0028)           # ~300m around orphan clusters
            if slug in cell_of:
                pat = pat.intersection(cell_of[slug])              # confine to this hood's cell
            geom = unary_union([geom, pat])
        geom = geom.buffer(0).simplify(SIMPLIFY, preserve_topology=True).buffer(0)
        if geom.is_empty or not geom.is_valid:
            print("WARN geometry issue for", slug, explain_validity(geom))
        out.append({"slug": slug, "display": disp_of[slug], "rings": to_rings(geom)})

    json.dump(out, open(OUT, "w"), indent=1)
    npts = sum(len(r) for n in out for r in n["rings"])
    print(f"wrote {len(out)} Brooklyn neighborhoods, {npts} total points -> {os.path.relpath(OUT)}")
    for n in sorted(out, key=lambda n: -sum(len(r) for r in n["rings"]))[:6]:
        print(f"   {n['slug']:<24} {sum(len(r) for r in n['rings'])} pts, {len(n['rings'])} ring(s)")


if __name__ == "__main__":
    main()
