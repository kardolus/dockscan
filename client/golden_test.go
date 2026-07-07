package client

import (
	"encoding/csv"
	"encoding/json"
	"os"
	"testing"

	"github.com/kardolus/dockscan/types"
)

// TestGoldenNYCAssignment is the Phase-0 de-risk gate: it proves that loading the
// neighborhood set from a FILE (the new runtime path that replaces //go:embed) and
// running assignNeighborhood reproduces the deployed ingester's output byte-for-byte.
// The golden artifacts in testdata/golden/nyc are a recorded live NYC station_information
// feed + the station→neighborhood assignment captured from the production DB. Any
// mismatch means the parameterization silently changed NYC, and must fail the build.
func TestGoldenNYCAssignment(t *testing.T) {
	const dir = "../testdata/golden/nyc/"

	// Neighborhoods via the runtime file loader (the new path) — same bytes as the embed.
	ns, err := LoadNeighborhoodsFromFile("neighborhoods.json")
	if err != nil {
		t.Fatalf("load neighborhoods from file: %v", err)
	}

	raw, err := os.ReadFile(dir + "station_information.json")
	if err != nil {
		t.Fatalf("read golden station_information: %v", err)
	}
	var si types.StationInformation
	if err := json.Unmarshal(raw, &si); err != nil {
		t.Fatalf("parse station_information: %v", err)
	}
	got := make(map[string]string, len(si.Data.Stations))
	for _, s := range si.Data.Stations {
		got[s.StationID] = assignNeighborhood(ns, s.Lat, s.Lon)
	}

	f, err := os.Open(dir + "assignment.csv")
	if err != nil {
		t.Fatalf("open golden assignment: %v", err)
	}
	defer f.Close()
	rows, err := csv.NewReader(f).ReadAll()
	if err != nil {
		t.Fatalf("read golden assignment: %v", err)
	}

	var mismatches, notInFeed int
	for i, row := range rows {
		if i == 0 || len(row) < 2 {
			continue // header
		}
		id, want := row[0], row[1]
		g, ok := got[id]
		if !ok {
			notInFeed++ // station-set drift between the two snapshots; not a logic error
			continue
		}
		if g != want {
			if mismatches < 10 {
				t.Errorf("station %s: assignment %q != golden %q", id, g, want)
			}
			mismatches++
		}
	}
	if mismatches > 0 {
		t.Errorf("%d of %d golden assignments differ — NYC behavior changed", mismatches, len(rows)-1)
	}
	t.Logf("golden NYC: %d stations checked, %d mismatches, %d not in current feed",
		len(rows)-1, mismatches, notInFeed)
}

// TestGoldenNYCEbikes guards the e-bike adapter: on the Lyft NYC feed (no
// num_bikes_available_types array), Station.Ebikes() must equal num_ebikes_available
// for every station, so the new normalization keeps NYC byte-identical.
func TestGoldenNYCEbikes(t *testing.T) {
	raw, err := os.ReadFile("../testdata/golden/nyc/station_status.json")
	if err != nil {
		t.Fatalf("read golden station_status: %v", err)
	}
	var ss types.StationStatus
	if err := json.Unmarshal(raw, &ss); err != nil {
		t.Fatalf("parse station_status: %v", err)
	}
	if len(ss.Data.Stations) == 0 {
		t.Fatal("no stations in golden status")
	}
	for _, s := range ss.Data.Stations {
		if got := s.Ebikes(); got != s.NumEbikesAvailable {
			t.Fatalf("station %s: Ebikes()=%d != num_ebikes_available=%d", s.StationID, got, s.NumEbikesAvailable)
		}
	}
	t.Logf("e-bike adapter byte-identical across %d NYC stations", len(ss.Data.Stations))
}
