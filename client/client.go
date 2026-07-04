package client

import (
	"database/sql"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/kardolus/citi-bike-dock-tracker/http"
	"github.com/kardolus/citi-bike-dock-tracker/metrics"
	"github.com/kardolus/citi-bike-dock-tracker/types"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	_ "github.com/lib/pq"
)

// BBox is a geographic bounding box used to filter stations by location.
type BBox struct {
	MinLat, MinLon, MaxLat, MaxLon float64
}

func (b BBox) contains(lat, lon float64) bool {
	return lat >= b.MinLat && lat <= b.MaxLat && lon >= b.MinLon && lon <= b.MaxLon
}

const (
	DefaultInterval        = 60 // in seconds
	DefaultServiceURL      = "https://gbfs.citibikenyc.com"
	ErrEmptyResponse       = "empty response"
	GoogleMapsQuery        = "https://www.google.com/maps/?q=%f,%f"
	StationInformationPath = "/gbfs/en/station_information.json"
	StationStatusPath      = "/gbfs/en/station_status.json"
)

type TimeProvider interface {
	Now() time.Time
}

type RealTime struct{}

func (RealTime) Now() time.Time {
	location, _ := time.LoadLocation("America/New_York")
	return time.Now().In(location)
}

// Ensure RealTime implements TimeProvider interface
var _ TimeProvider = &RealTime{}

type Client struct {
	caller          http.Caller
	stationMap      map[string]types.StationEntity
	neighborhood    map[string]string // station_id -> neighborhood slug (empty when not in neighborhood mode)
	electricTypes   map[string]bool   // PBSC e-bike vehicle_type_ids (empty for every other operator)
	timeProvider    TimeProvider
	interval        int
	serviceURL      string
	statusURL       string // full station_status URL; overrides serviceURL+path when set
	feedFormat      string // "gbfs" (default) or "tfl" (London BikePoint, non-GBFS)
	currentDate     time.Time
	outputDirectory string
}

type ClientBuilder struct {
	caller             http.Caller
	stationMap         map[string]types.StationEntity
	timeProvider       TimeProvider
	interval           int
	serviceURL         string
	statusURL          string // full station_status URL (per-city, e.g. Lyft /gbfs/2.3/dca-cabi/en/...)
	infoURL            string // full station_information URL
	vehicleTypesURL    string // full vehicle_types.json URL (PBSC/Bicing e-bike classification)
	feedFormat         string // "gbfs" (default) or "tfl" (London Santander Cycles BikePoint)
	filteredIDs        map[string]bool
	bbox               *BBox
	neighborhoods      []Neighborhood
	requireStationCode bool // drop feed entries with no short_name (e.g. Divvy "Public Rack" corrals — not real docking stations)
	outputDirectory    string
}

func NewClientBuilder() *ClientBuilder {
	return &ClientBuilder{
		caller:       http.New(),
		stationMap:   make(map[string]types.StationEntity),
		interval:     DefaultInterval,
		timeProvider: RealTime{},
		serviceURL:   DefaultServiceURL,
		filteredIDs:  make(map[string]bool),
	}
}

// WithCaller overwrites the default http caller
func (b *ClientBuilder) WithCaller(caller http.Caller) *ClientBuilder {
	b.caller = caller
	return b
}

// WithIDFilter adds a filter to only look for specific station IDs
func (b *ClientBuilder) WithIDFilter(ids []string) *ClientBuilder {
	for _, id := range ids {
		b.filteredIDs[id] = true
	}
	return b
}

// WithBBox restricts stations to those within a geographic bounding box
func (b *ClientBuilder) WithBBox(box BBox) *ClientBuilder {
	b.bbox = &box
	return b
}

// WithNeighborhoods tags each station with its neighborhood slug (assigned once
// at Build time, then memoized). The full city-wide set + the nearest-centroid
// fallback in assignNeighborhood means every station in the service area gets a
// neighborhood, so none are dropped.
func (b *ClientBuilder) WithNeighborhoods(ns []Neighborhood) *ClientBuilder {
	b.neighborhoods = ns
	return b
}

// WithRequireStationCode, when enabled, drops feed entries that have no short_name
// station code. Some systems (notably Divvy) list on-street "Public Rack"/corral
// parking spots alongside real docking stations; those lack a short_name and are
// empty by design, so counting them as (empty) stations is misleading. Opt-in per
// city — the default keeps every station.
func (b *ClientBuilder) WithRequireStationCode(require bool) *ClientBuilder {
	b.requireStationCode = require
	return b
}

// WithInterval overwrites the default interval
func (b *ClientBuilder) WithInterval(interval int) *ClientBuilder {
	b.interval = interval
	return b
}

// WithOutputDirectory specifies the directory to which CSV files should be written
func (b *ClientBuilder) WithOutputDirectory(dir string) *ClientBuilder {
	b.outputDirectory = dir
	return b
}

// WithServiceURL overwrites the default service URL (base; combined with the
// default station_information/station_status paths).
func (b *ClientBuilder) WithServiceURL(url string) *ClientBuilder {
	b.serviceURL = url
	return b
}

// WithFeedURLs sets the full station_information and station_status URLs directly,
// overriding serviceURL+path. Needed because operators lay out their GBFS paths
// differently (Lyft `/gbfs/2.3/<system>/en/...`, Smovengo `/opendata/...`). Empty
// values fall back to serviceURL + the default paths.
func (b *ClientBuilder) WithFeedURLs(infoURL, statusURL string) *ClientBuilder {
	b.infoURL = infoURL
	b.statusURL = statusURL
	return b
}

// WithVehicleTypesURL sets the GBFS vehicle_types.json URL. Only PBSC feeds (Bicing)
// need it: they report the mechanical/e-bike split in station_status as opaque
// vehicle_type_ids, and this file says which of those ids are e-bikes. Unset for
// every other operator (Lyft/Smovengo carry the e-bike count inline).
func (b *ClientBuilder) WithVehicleTypesURL(url string) *ClientBuilder {
	b.vehicleTypesURL = url
	return b
}

// WithFeedFormat selects the feed parser: "gbfs" (default, every GBFS operator) or "tfl"
// (London Santander Cycles — TfL's non-GBFS BikePoint API). For "tfl" the info/status URLs
// both point at the BikePoint endpoint.
func (b *ClientBuilder) WithFeedFormat(format string) *ClientBuilder {
	if format != "" {
		b.feedFormat = format
	}
	return b
}

// WithTimeProvider overwrites the default time provider
func (b *ClientBuilder) WithTimeProvider(provider TimeProvider) *ClientBuilder {
	b.timeProvider = provider
	return b
}

// Build creates the Client instance
func (b *ClientBuilder) Build() (*Client, error) {
	stationInfo, err := b.getStationInformation()
	if err != nil {
		return nil, err
	}

	// PBSC e-bike classification (Bicing): fetch vehicle_types.json once at build.
	// Non-fatal — on failure we just fall back to no e-bike split for that feed.
	var electricTypes map[string]bool
	if b.vehicleTypesURL != "" {
		if vt, err := b.getVehicleTypes(); err != nil {
			log.Printf("vehicle_types fetch failed (non-fatal, no e-bike split): %v", err)
		} else {
			electricTypes = vt.ElectricBicycleTypes()
			log.Printf("vehicle_types: %d e-bike vehicle_type_ids", len(electricTypes))
		}
	}

	neighborhood := make(map[string]string)
	droppedNoCode := 0
	for _, station := range stationInfo.Data.Stations {
		// include unless an ID filter, bbox, or neighborhood set excludes it
		if len(b.filteredIDs) > 0 {
			if _, ok := b.filteredIDs[station.StationID]; !ok {
				continue
			}
		}
		// drop non-docking entries (no short_name station code) when opted in —
		// e.g. Divvy's "Public Rack"/corral parking spots, which are empty by design.
		if b.requireStationCode && station.ShortName.String() == "" {
			droppedNoCode++
			continue
		}
		if b.bbox != nil && !b.bbox.contains(station.Lat, station.Lon) {
			continue
		}
		if len(b.neighborhoods) > 0 {
			slug := assignNeighborhood(b.neighborhoods, station.Lat, station.Lon)
			if slug == "" {
				continue // not in any curated neighborhood
			}
			neighborhood[station.StationID] = slug
		}
		b.stationMap[station.StationID] = station
	}
	if b.requireStationCode {
		log.Printf("station-code filter: kept %d, dropped %d entries without a short_name (non-docking racks)", len(b.stationMap), droppedNoCode)
	}

	return &Client{
		caller:          b.caller,
		stationMap:      b.stationMap,
		neighborhood:    neighborhood,
		electricTypes:   electricTypes,
		interval:        b.interval,
		timeProvider:    b.timeProvider,
		serviceURL:      b.serviceURL,
		statusURL:       b.statusURL,
		feedFormat:      b.feedFormat,
		currentDate:     startOfDay(b.timeProvider.Now()),
		outputDirectory: b.outputDirectory,
	}, nil
}

func (b *ClientBuilder) getStationInformation() (types.StationInformation, error) {
	url := b.infoURL
	if url == "" {
		url = b.serviceURL + StationInformationPath
	}
	raw, err := b.caller.Get(url)
	if err != nil {
		return types.StationInformation{}, err
	}
	if raw == nil {
		return types.StationInformation{}, errors.New(ErrEmptyResponse)
	}

	if b.feedFormat == "tfl" {
		return types.TflToInformation(raw)
	}

	var response types.StationInformation
	if err := processResponse(raw, &response); err != nil {
		return types.StationInformation{}, err
	}

	return response, nil
}

func (b *ClientBuilder) getVehicleTypes() (types.VehicleTypes, error) {
	raw, err := b.caller.Get(b.vehicleTypesURL)
	if err != nil {
		return types.VehicleTypes{}, err
	}
	var response types.VehicleTypes
	if err := processResponse(raw, &response); err != nil {
		return types.VehicleTypes{}, err
	}
	return response, nil
}

// ParseStationData fetches station status information from the Citi Bike API and combines
// it with pre-fetched station information to create a set of normalized data.
//
// The normalized data consists of details such as station ID, name, status, capacity, and
// the number of available bikes, e-bikes, docks, and scooters, as well as operational status flags.
//
// The function returns a NormalizedStationData instance containing the collected information,
// or an error if fetching or processing the data fails. Note that the function only includes
// stations which have corresponding entries in the pre-fetched station information data.
func (c *Client) ParseStationData() (types.NormalizedStationData, error) {
	var result types.NormalizedStationData

	statusData, err := c.getStationStatus()
	if err != nil {
		return types.NormalizedStationData{}, err
	}

	for _, stationStatus := range statusData.Data.Stations {
		if stationInfo, ok := c.stationMap[stationStatus.StationID]; ok {
			item := normalizeStationData(stationStatus, stationInfo, c.electricTypes)
			item.Neighborhood = c.neighborhood[stationStatus.StationID]
			result.Stations = append(result.Stations, item)
		}
	}

	result.TimeStamp = c.timeProvider.Now()

	return result, nil
}

// PrintStationDataJSONL fetches station status information from the Citi Bike API and combines
// it with pre-fetched station information to create a set of normalized data. The normalized data
// is printed to stdout in the JSONL format.
//
// The function runs indefinitely, fetching new data every minute. To stop the function, you must
// interrupt the program manually.
func (c *Client) PrintStationDataJSONL() {
	for {
		stationData, err := c.gatherStationData()
		if err != nil {
			continue
		}

		for _, data := range stationData {
			jsonl, err := json.Marshal(data)
			if err != nil {
				continue
			}
			fmt.Println(string(jsonl))
		}

		time.Sleep(time.Duration(c.interval) * time.Second)
	}
}

// PrintStationDataCSV gathers station data periodically according to the client's interval
// and prints it to the standard output (stdout) in CSV format. The CSV data includes a header row,
// and each subsequent row represents the current state of a station.
// The fields are StationID, Name, Longitude, Latitude, Location, Status, BikesAvailable,
// EBikesAvailable, BikesDisabled, DocksAvailable, DocksDisabled, IsReturning, IsRenting,
// IsInstalled, and TimeStamp. In case of an error while gathering data, the function continues with
// the next iteration after the sleep interval. If writing to the CSV writer fails, the function logs
// the error and exits. The function runs indefinitely, and each iteration is separated by a sleep
// interval defined by the client.
func (c *Client) PrintStationDataCSV(excludeColumns []string) {
	var w *csv.Writer

	if c.outputDirectory == "" {
		w = csv.NewWriter(os.Stdout)
	} else {
		w = createNewWriter(c.currentDate, c.outputDirectory)
	}

	headers := []string{
		"ID",
		"Name",
		"Longitude",
		"Latitude",
		"Location",
		"Status",
		"BikesAvailable",
		"EBikesAvailable",
		"BikesDisabled",
		"DocksAvailable",
		"DocksDisabled",
		"IsReturning",
		"IsRenting",
		"IsInstalled",
		"TimeStamp",
	}

	// Prepare headers
	var finalHeaders []string
	for _, h := range headers {
		if !contains(excludeColumns, h) {
			finalHeaders = append(finalHeaders, h)
		}
	}

	_ = w.Write(finalHeaders)

	for {
		currentDay := startOfDay(c.timeProvider.Now())
		if currentDay.After(c.currentDate) {
			w.Flush()
			w = createNewWriter(currentDay, c.outputDirectory)
			_ = w.Write(finalHeaders)
			c.currentDate = currentDay
		}

		stationData, err := c.gatherStationData()
		if err != nil {
			continue
		}

		for _, data := range stationData {
			var record []string
			if !contains(excludeColumns, "ID") {
				record = append(record, data.Station.ID)
			}
			if !contains(excludeColumns, "Name") {
				record = append(record, data.Station.Name)
			}
			if !contains(excludeColumns, "Longitude") {
				record = append(record, fmt.Sprint(data.Station.Longitude))
			}
			if !contains(excludeColumns, "Latitude") {
				record = append(record, fmt.Sprint(data.Station.Latitude))
			}
			if !contains(excludeColumns, "Location") {
				record = append(record, data.Station.Location)
			}
			if !contains(excludeColumns, "BikesAvailable") {
				record = append(record, fmt.Sprint(data.Station.BikesAvailable))
			}
			if !contains(excludeColumns, "EBikesAvailable") {
				record = append(record, fmt.Sprint(data.Station.EBikesAvailable))
			}
			if !contains(excludeColumns, "BikesDisabled") {
				record = append(record, fmt.Sprint(data.Station.BikesDisabled))
			}
			if !contains(excludeColumns, "DocksAvailable") {
				record = append(record, fmt.Sprint(data.Station.DocksAvailable))
			}
			if !contains(excludeColumns, "DocksDisabled") {
				record = append(record, fmt.Sprint(data.Station.DocksDisabled))
			}
			if !contains(excludeColumns, "IsReturning") {
				record = append(record, fmt.Sprint(data.Station.IsReturning))
			}
			if !contains(excludeColumns, "IsRenting") {
				record = append(record, fmt.Sprint(data.Station.IsRenting))
			}
			if !contains(excludeColumns, "IsInstalled") {
				record = append(record, fmt.Sprint(data.Station.IsInstalled))
			}
			if !contains(excludeColumns, "TimeStamp") {
				record = append(record, data.TimeStamp.Format(time.RFC3339))
			}
			_ = w.Write(record)
		}
		w.Flush()

		time.Sleep(time.Duration(c.interval) * time.Second)
	}
}

// Helper function to check if a slice contains a string
func contains(slice []string, str string) bool {
	for _, s := range slice {
		if s == str {
			return true
		}
	}
	return false
}

func (c *Client) gatherStationData() ([]types.NormalizedStationDataTS, error) {
	var stationData []types.NormalizedStationDataTS

	statusData, err := c.getStationStatus()
	if err != nil {
		return nil, err
	}

	now := c.timeProvider.Now()

	for _, stationStatus := range statusData.Data.Stations {
		if stationInfo, ok := c.stationMap[stationStatus.StationID]; ok {
			item := normalizeStationData(stationStatus, stationInfo, c.electricTypes)
			item.Neighborhood = c.neighborhood[stationStatus.StationID]
			data := types.NormalizedStationDataTS{
				Station:   item,
				TimeStamp: now,
			}
			stationData = append(stationData, data)
		}
	}

	return stationData, nil
}

func (c *Client) getStationStatus() (types.StationStatus, error) {
	url := c.statusURL
	if url == "" {
		url = c.serviceURL + StationStatusPath
	}
	raw, err := c.caller.Get(url)
	if err != nil {
		return types.StationStatus{}, err
	}
	if raw == nil {
		return types.StationStatus{}, errors.New(ErrEmptyResponse)
	}

	if c.feedFormat == "tfl" {
		return types.TflToStatus(raw)
	}

	var response types.StationStatus
	if err := processResponse(raw, &response); err != nil {
		return types.StationStatus{}, err
	}

	return response, nil
}

func createNewWriter(currentDay time.Time, dir string) *csv.Writer {
	filename := filepath.Join(dir, currentDay.Format("2006-01-02")+".csv")
	file, _ := os.Create(filename)

	return csv.NewWriter(file)
}

func normalizeStationData(stationStatus types.Station, stationInfo types.StationEntity, electricTypes map[string]bool) types.NormalizedStation {
	var item types.NormalizedStation

	item.ID = stationStatus.StationID
	item.Name = stationInfo.Name.String()
	item.Longitude = stationInfo.Lon
	item.Latitude = stationInfo.Lat
	item.Location = fmt.Sprintf(GoogleMapsQuery, stationInfo.Lat, stationInfo.Lon)
	item.BikesAvailable = stationStatus.Bikes()
	item.EBikesAvailable = stationStatus.EbikesWith(electricTypes)
	item.BikesDisabled = stationStatus.Disabled()
	item.DocksAvailable = stationStatus.NumDocksAvailable
	item.DocksDisabled = stationStatus.NumDocksDisabled
	item.ScootersAvailable = stationStatus.NumScootersAvailable
	item.ScootersUnavailable = stationStatus.NumScootersUnavailable
	item.IsReturning = stationStatus.IsReturning == 1
	item.IsRenting = stationStatus.IsRenting == 1
	item.IsInstalled = stationStatus.IsInstalled == 1

	return item
}

func processResponse(raw []byte, v interface{}) error {
	if raw == nil {
		return errors.New(ErrEmptyResponse)
	}

	if err := json.Unmarshal(raw, v); err != nil {
		return fmt.Errorf("failed to decode response: %w", err)
	}

	return nil
}

func startOfDay(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, t.Location())
}

const createDockStatusTable = `
CREATE TABLE IF NOT EXISTS dock_status (
    station_id           text        NOT NULL,
    name                 text        NOT NULL,
    longitude            double precision,
    latitude             double precision,
    bikes_available      integer,
    ebikes_available     integer,
    bikes_disabled       integer,
    docks_available      integer,
    docks_disabled       integer,
    scooters_available   integer,
    scooters_unavailable integer,
    is_returning         boolean,
    is_renting           boolean,
    is_installed         boolean,
    neighborhood         text,
    ts                   timestamptz NOT NULL
);
-- self-migrate older tables that predate the neighborhood column
ALTER TABLE dock_status ADD COLUMN IF NOT EXISTS neighborhood text;
-- Lean, TimescaleDB-friendly index set: one (station_id, ts DESC) backs both the
-- "latest per station" Now query and the per-station LAG window scans; a partial
-- (neighborhood, ts) backs the neighborhood filter (NULL = uncurated stations are
-- excluded); a BRIN on ts gives cheap range/retention scans. The two older
-- redundant indexes ((station_id, ts) ASC and a plain (ts) btree) are dropped.
DROP INDEX IF EXISTS dock_status_station_ts_idx;
DROP INDEX IF EXISTS dock_status_ts_idx;
CREATE INDEX IF NOT EXISTS dock_status_station_ts_desc_idx ON dock_status (station_id, ts DESC);
CREATE INDEX IF NOT EXISTS dock_status_nbhd_ts_idx ON dock_status (neighborhood, ts) WHERE neighborhood IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_dock_status_ts_brin ON dock_status USING brin (ts);
`

// timescaleSetup converts dock_status to a compressed TimescaleDB hypertable. It
// runs only when the timescaledb extension is available, is idempotent on an
// already-converted DB, and is treated as non-fatal — on plain Postgres (or if
// anything here fails) ingestion still proceeds, just uncompressed.
const timescaleSetup = `
CREATE EXTENSION IF NOT EXISTS timescaledb;
SELECT create_hypertable('dock_status','ts', chunk_time_interval => INTERVAL '1 day', if_not_exists => true, migrate_data => true);
ALTER TABLE dock_status SET (timescaledb.compress, timescaledb.compress_segmentby='station_id', timescaledb.compress_orderby='ts DESC');
SELECT add_compression_policy('dock_status', INTERVAL '7 days', if_not_exists => true);
`

// IngestPostgres runs the polling loop, writing each tracked station's status to
// the dock_status table on every interval. It creates the table if missing and
// runs indefinitely. Health is surfaced via the metrics package.
func (c *Client) IngestPostgres(dsn string) error {
	if dsn == "" {
		return errors.New("postgres DSN is empty (set DATABASE_URL)")
	}
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		return fmt.Errorf("open postgres: %w", err)
	}
	defer db.Close()
	db.SetMaxOpenConns(2)
	// recycle connections so a Postgres restart doesn't wedge ingestion on a
	// stale pooled conn (lib/pq won't otherwise evict broken conns for a while)
	db.SetConnMaxLifetime(5 * time.Minute)
	db.SetConnMaxIdleTime(5 * time.Minute)

	if err := db.Ping(); err != nil {
		return fmt.Errorf("ping postgres: %w", err)
	}
	if _, err := db.Exec(createDockStatusTable); err != nil {
		return fmt.Errorf("ensure schema: %w", err)
	}
	// Guard against a misconfigured deploy (a city's ingester pointed at another city's
	// DB): stamp/assert this DB's city_id. Fatal on mismatch so we never corrupt data.
	if err := ensureCityID(db); err != nil {
		return err
	}
	// If TimescaleDB is available, make dock_status a compressed hypertable.
	// Non-fatal: plain Postgres (or any failure here) just means uncompressed rows.
	var hasTimescale bool
	if err := db.QueryRow(
		"SELECT EXISTS(SELECT 1 FROM pg_available_extensions WHERE name = 'timescaledb')",
	).Scan(&hasTimescale); err != nil {
		log.Printf("timescaledb availability check failed (non-fatal): %v", err)
	}
	if hasTimescale {
		if _, err := db.Exec(timescaleSetup); err != nil {
			log.Printf("timescaledb setup failed (non-fatal, continuing uncompressed): %v", err)
		} else {
			log.Printf("timescaledb: dock_status is a compressed hypertable")
		}
		// Optional 90-day-style retention: drop chunks older than RETENTION_DAYS so the
		// table stays bounded. Opt-in via env — NYC leaves it unset and instead uses its
		// archive-to-cold-drive prune CronJob; the chart-deployed cities set it to 90.
		if days := strings.TrimSpace(os.Getenv("RETENTION_DAYS")); days != "" {
			if n, err := strconv.Atoi(days); err != nil || n <= 0 {
				log.Printf("invalid RETENTION_DAYS=%q (want a positive integer); skipping retention policy", days)
			} else if _, err := db.Exec(fmt.Sprintf(
				"SELECT add_retention_policy('dock_status', INTERVAL '%d days', if_not_exists => true)", n)); err != nil {
				log.Printf("retention policy setup failed (non-fatal): %v", err)
			} else {
				log.Printf("timescaledb: retention policy drops chunks older than %d days", n)
			}
		}
	}
	log.Printf("ingesting to postgres every %ds (%d stations tracked)", c.interval, len(c.stationMap))

	for {
		metrics.IncPolls()
		stationData, err := c.gatherStationData()
		if err != nil {
			metrics.IncFetchError()
			log.Printf("fetch error: %v", err)
			time.Sleep(time.Duration(c.interval) * time.Second)
			continue
		}
		if err := c.insertBatch(db, stationData); err != nil {
			metrics.IncDBError()
			log.Printf("db write error: %v", err)
		} else {
			metrics.AddRows(len(stationData))
			metrics.SetStations(len(stationData))
			metrics.MarkSuccess(c.timeProvider.Now())
		}
		time.Sleep(time.Duration(c.interval) * time.Second)
	}
}

// ensureCityID stamps app_metadata.city_id with the CITY_ID env on first run and asserts
// it never changes — so a Paris ingester pointed at the CDMX DB (or similar) fails fast
// instead of writing into the wrong database. No-op when CITY_ID is unset (the original
// NYC deploy), so it's safe to roll out incrementally.
func ensureCityID(db *sql.DB) error {
	cityID := os.Getenv("CITY_ID")
	if cityID == "" {
		return nil
	}
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS app_metadata (key text PRIMARY KEY, value text NOT NULL)`); err != nil {
		return fmt.Errorf("app_metadata: %w", err)
	}
	var existing string
	switch err := db.QueryRow("SELECT value FROM app_metadata WHERE key='city_id'").Scan(&existing); err {
	case sql.ErrNoRows:
		_, err = db.Exec("INSERT INTO app_metadata(key,value) VALUES('city_id',$1)", cityID)
		return err
	case nil:
		if existing != cityID {
			return fmt.Errorf("city_id mismatch: this DB is %q but CITY_ID=%q — wrong database?", existing, cityID)
		}
		return nil
	default:
		return err
	}
}

// nullable maps an empty string to a SQL NULL (used for neighborhood when not
// in neighborhood mode).
func nullable(s string) interface{} {
	if s == "" {
		return nil
	}
	return s
}

func (c *Client) insertBatch(db *sql.DB, data []types.NormalizedStationDataTS) error {
	if len(data) == 0 {
		return nil
	}
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	stmt, err := tx.Prepare(`INSERT INTO dock_status
        (station_id,name,longitude,latitude,bikes_available,ebikes_available,bikes_disabled,
         docks_available,docks_disabled,scooters_available,scooters_unavailable,
         is_returning,is_renting,is_installed,neighborhood,ts)
        VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16)`)
	if err != nil {
		_ = tx.Rollback()
		return err
	}
	defer stmt.Close()
	for _, d := range data {
		s := d.Station
		if _, err := stmt.Exec(s.ID, s.Name, s.Longitude, s.Latitude, s.BikesAvailable,
			s.EBikesAvailable, s.BikesDisabled, s.DocksAvailable, s.DocksDisabled,
			s.ScootersAvailable, s.ScootersUnavailable, s.IsReturning, s.IsRenting,
			s.IsInstalled, nullable(s.Neighborhood), d.TimeStamp); err != nil {
			_ = tx.Rollback()
			return err
		}
	}
	return tx.Commit()
}
