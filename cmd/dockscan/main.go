package main

import (
	"encoding/json"
	"fmt"
	"github.com/kardolus/citi-bike-dock-tracker/client"
	"github.com/kardolus/citi-bike-dock-tracker/metrics"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"os"
	"strconv"
	"strings"
	_ "time/tzdata" // embed the tz database so LoadLocation works in distroless
)

var (
	GitCommit   string
	GitVersion  string
	ServiceURL  string
	ids         []string
	exclude     []string
	interval    int
	csv         bool
	output      string
	postgres    bool
	area        string
	bbox        string
	metricsAddr string
)

// curatedArea is the special --area value that enables the curated
// multi-neighborhood set (see client/neighborhoods.json) instead of a bbox.
const curatedArea = "bk-curated"

// namedAreas maps a friendly name to a bounding box.
var namedAreas = map[string]client.BBox{
	// Red Hook, Brooklyn — the envelope of the 19 hand-curated Red Hook stations
	// (peninsula + the Columbia St corridor), padded slightly so new stations in
	// the area are picked up. Excludes the Carroll Gardens / Gowanus stations to
	// the NE (east of ~-74.000).
	"redhook": {MinLat: 40.6704, MinLon: -74.018, MaxLat: 40.6869, MaxLon: -74.0003},
}

func main() {
	var rootCmd = &cobra.Command{
		Use:   "dockscan",
		Short: "A CLI for tracking Citibike dock station status.",
		Long: "dockscan is a Command Line Interface (CLI) application " +
			"built to track the status of Citibike dock stations. " +
			"It retrieves data from the Citibike dock stations status API " +
			"and maps station IDs to human-readable names using the station information API. ",
	}

	viper.AutomaticEnv()

	var cmdInfo = &cobra.Command{
		Use:   "info",
		Short: "Retrieve and display Citibike dock station status.",
		Long:  "The 'info' command retrieves and displays the current status of Citibike dock stations.",
		RunE:  runInfo,
	}

	cmdInfo.Flags().StringSliceVar(&ids, "id", []string{}, "Filter dock station status by IDs")

	rootCmd.AddCommand(cmdInfo)

	var cmdVersion = &cobra.Command{
		Use:   "version",
		Short: "Displays the version of dockscan.",
		Long:  "The 'version' command displays the current version of the dockscan CLI tool.",
		RunE:  runVersion,
	}
	rootCmd.AddCommand(cmdVersion)

	var cmdTs = &cobra.Command{
		Use:   "ts",
		Short: "Retrieve and display Citibike dock station status with timestamps in JSONL format.",
		Long:  "The 'ts' command retrieves and displays the current status of Citibike dock stations with timestamps in JSON Lines (JSONL) format.",
		RunE:  runTs,
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			if cmd.Flags().Changed("exclude") && !cmd.Flags().Changed("csv") {
				return fmt.Errorf("--exclude requires --csv")
			}
			if cmd.Flags().Changed("output") && !cmd.Flags().Changed("csv") {
				return fmt.Errorf("--output requires --csv")
			}
			return nil
		},
	}

	cmdTs.Flags().StringSliceVar(&ids, "id", []string{}, "Filter dock station status by IDs")
	cmdTs.Flags().IntVar(&interval, "interval", 60, "Set the time interval (in seconds) between fetching station status updates")
	cmdTs.Flags().BoolVar(&csv, "csv", false, "Output station status in CSV format")
	cmdTs.Flags().StringSliceVar(&exclude, "exclude", []string{}, "Exclude columns from the CSV output")
	cmdTs.Flags().StringVar(&output, "output", "", "Directory to save the output")
	cmdTs.Flags().BoolVar(&postgres, "postgres", false, "Write station status to Postgres (DSN from DATABASE_URL)")
	cmdTs.Flags().StringVar(&area, "area", "", "Named area to track: 'redhook' (bbox) or 'bk-curated' (multi-neighborhood)")
	cmdTs.Flags().StringVar(&bbox, "bbox", "", "Bounding box filter: minLat,minLon,maxLat,maxLon")
	cmdTs.Flags().StringVar(&metricsAddr, "metrics-addr", ":2112", "Address for the /metrics + /healthz server (empty to disable)")

	rootCmd.AddCommand(cmdTs)

	if err := rootCmd.Execute(); err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
}

func runInfo(cmd *cobra.Command, args []string) error {
	builder := client.NewClientBuilder()

	if ServiceURL != "" {
		builder = builder.WithServiceURL(ServiceURL)
	}

	if len(ids) > 0 {
		builder = builder.WithIDFilter(ids)
	}

	c, err := builder.Build()

	if err != nil {
		return err
	}

	data, err := c.ParseStationData()
	if err != nil {
		return err
	}

	result, err := json.Marshal(&data)
	if err != nil {
		return err
	}

	fmt.Println(string(result))

	return nil
}

func runTs(cmd *cobra.Command, args []string) error {
	builder := client.NewClientBuilder()

	// Per-city config via env (one image serves every city). GBFS_URL overrides the
	// base; GBFS_STATION_INFORMATION_URL / GBFS_STATION_STATUS_URL override the full
	// feed URLs (operators lay out paths differently). All optional — unset = NYC.
	if v := os.Getenv("GBFS_URL"); v != "" {
		ServiceURL = v
	}
	if ServiceURL != "" {
		builder = builder.WithServiceURL(ServiceURL)
	}
	// FEED_FORMAT selects the parser: "gbfs" (default) or "tfl" (London Santander Cycles —
	// TfL's non-GBFS BikePoint API; info+status both read the BikePoint endpoint).
	builder = builder.WithFeedFormat(os.Getenv("FEED_FORMAT"))
	// REQUIRE_STATION_CODE=true drops feed entries with no short_name (e.g. Divvy
	// "Public Rack"/corral parking spots) so they aren't tracked as docking stations.
	if v := os.Getenv("REQUIRE_STATION_CODE"); v == "true" || v == "1" {
		builder = builder.WithRequireStationCode(true)
	}
	if infoURL, statusURL := os.Getenv("GBFS_STATION_INFORMATION_URL"), os.Getenv("GBFS_STATION_STATUS_URL"); infoURL != "" || statusURL != "" {
		// TfL BikePoint is public; an optional free app_key just raises rate limits.
		if key := os.Getenv("TFL_APP_KEY"); key != "" {
			infoURL = appendQuery(infoURL, "app_key="+key)
			statusURL = appendQuery(statusURL, "app_key="+key)
		}
		builder = builder.WithFeedURLs(infoURL, statusURL)
	}
	// PBSC feeds (Bicing) need vehicle_types.json to know which vehicle_type_ids are
	// e-bikes; every other operator carries the e-bike count inline and leaves this unset.
	if v := os.Getenv("GBFS_VEHICLE_TYPES_URL"); v != "" {
		builder = builder.WithVehicleTypesURL(v)
	}

	if len(ids) > 0 {
		builder = builder.WithIDFilter(ids)
	}

	if interval > 0 {
		builder = builder.WithInterval(interval)
	}

	if output != "" {
		builder = builder.WithOutputDirectory(output)
	}

	// Neighborhood assignment source, in precedence order:
	//   1. NEIGHBORHOODS_PATH env  → load that file (the per-city ConfigMap mount).
	//   2. --area bk-curated       → the embedded NYC default (legacy / fallback).
	//   3. otherwise               → bbox / no neighborhood tagging.
	if path := os.Getenv("NEIGHBORHOODS_PATH"); path != "" {
		ns, err := client.LoadNeighborhoodsFromFile(path)
		if err != nil {
			return err
		}
		builder = builder.WithNeighborhoods(ns)
	} else if strings.ToLower(area) == curatedArea {
		ns, err := client.LoadNeighborhoods()
		if err != nil {
			return err
		}
		builder = builder.WithNeighborhoods(ns)
	} else {
		box, err := resolveBBox(area, bbox)
		if err != nil {
			return err
		}
		if box != nil {
			builder = builder.WithBBox(*box)
		}
	}

	c, err := builder.Build()
	if err != nil {
		return err
	}

	if metricsAddr != "" {
		metrics.Serve(metricsAddr)
	}

	if postgres {
		return c.IngestPostgres(os.Getenv("DATABASE_URL"))
	}

	if csv {
		c.PrintStationDataCSV(exclude)
	} else {
		c.PrintStationDataJSONL()
	}

	return nil
}

// resolveBBox turns --area (named preset) or --bbox (raw coords) into a BBox.
func resolveBBox(area, bbox string) (*client.BBox, error) {
	if area != "" {
		b, ok := namedAreas[strings.ToLower(area)]
		if !ok {
			return nil, fmt.Errorf("unknown --area %q", area)
		}
		return &b, nil
	}
	if bbox != "" {
		parts := strings.Split(bbox, ",")
		if len(parts) != 4 {
			return nil, fmt.Errorf("--bbox must be minLat,minLon,maxLat,maxLon")
		}
		var f [4]float64
		for i, p := range parts {
			v, err := strconv.ParseFloat(strings.TrimSpace(p), 64)
			if err != nil {
				return nil, fmt.Errorf("--bbox parse error: %w", err)
			}
			f[i] = v
		}
		return &client.BBox{MinLat: f[0], MinLon: f[1], MaxLat: f[2], MaxLon: f[3]}, nil
	}
	return nil, nil
}

func runVersion(cmd *cobra.Command, args []string) error {
	fmt.Printf("dockscan version %s (commit %s)\n", GitVersion, GitCommit)

	return nil
}

// appendQuery adds a query param to a URL (used for TfL's optional app_key). Empty URLs are
// left untouched so an unset info/status URL still falls back to the GBFS default path.
func appendQuery(url, q string) string {
	if url == "" {
		return ""
	}
	sep := "?"
	if strings.Contains(url, "?") {
		sep = "&"
	}
	return url + sep + q
}
