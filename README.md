# Citi Bike Dock Tracker

_NEW [Research on data collected using this repo](https://github.com/kardolus/opendata)_

## Table of Contents

1. [Introduction](#introduction)
    - [Output](#output)
2. [Installation](#installation)
    - [Apple M1 chips](#apple-m1-chips)
    - [macOS Intel chips](#macos-intel-chips)
    - [Linux (amd64)](#linux-amd64)
    - [Linux (arm64)](#linux-arm64)
    - [Windows (amd64)](#windows-amd64)
3. [Usage](#usage)
    - [Filtering by ID](#filtering-by-id)
    - [Time series data](#time-series-data)
    - [Excluding columns](#excluding-columns)
    - [Specify Output Directory](#specify-output-directory)
4. [Development](#development)
5. [Uninstallation](#uninstallation)
6. [Contributing](#contributing)

## Introduction

Welcome to the `Citi Bike Dock Tracker` repository, a robust tool designed for tracking, storing, and analyzing
time-series data relating to the status of CitiBike docking stations across New York City. This command-line interface (
CLI) tool provides granular details about each dock, including the number of available standard and electric bikes, the
number of open docks, and the progression of these variables over time.

The primary data source for this application is CitiBike's General Bikeshare Feed Specification (GBFS) data feeds. The
tracker retrieves real-time information from two distinct GBFS feeds:

1. The Station Status feed (`https://gbfs.citibikenyc.com/gbfs/en/station_status.json`), which provides live updates
   about each docking station's status, such as the number of available bikes, open docks, and more.
2. The Station Information feed (`https://gbfs.citibikenyc.com/gbfs/en/station_information.json`), which delivers
   essential information about each station, including its ID and human-readable name.

By consolidating and interpreting these two data sources, our CLI tool provides comprehensive, up-to-the-minute insights
into the operational state of CitiBike docking stations. Users can fetch data for all stations, filter by specific
station IDs, or even track time-series data for specified stations at user-defined intervals.

For maximum versatility, the `Citi Bike Dock Tracker` offers output in both JSON and CSV formats, catering to a wide
array of data analysis and visualization needs. This makes it an ideal solution for bike users, data analysts, urban
planners, and anyone else seeking to understand bike availability trends in real-time across New York City.

### Output

When you run the dockscan-cli, it produces a JSON output for each Citi Bike station. Here's an example of what one of
these JSON objects might look like:

```json
{
  "id": "5faf99b8-9046-450f-9d2a-d13279b3d016",
  "name": "Hoboken Ave at Monmouth St",
  "longitude": -74.04696375131607,
  "latitude": 40.73520838045357,
  "location": "https://www.google.com/maps/?q=40.735208,-74.046964",
  "status": "active",
  "bikesAvailable": 21,
  "eBikesAvailable": 7,
  "bikesDisabled": 4,
  "docksAvailable": 7,
  "docksDisabled": 0,
  "isReturning": true,
  "isRenting": true,
  "isInstalled": true
}
```

The output provides valuable information such as the station's name, its location (both in terms of longitude and
latitude and a Google Maps link), the status of the station, and detailed statistics about the number of available bikes
and docks.

## Installation

The installation steps differ depending on the type of chip your device uses. Below, you can find installation steps for
a variety of common systems:

### Apple M1 chips

```shell
curl -L -o dockscan https://github.com/kardolus/dockscan/releases/download/v1.2/dockscan-darwin-arm64 && chmod +x dockscan && sudo mv dockscan /usr/local/bin/
```

### macOS Intel chips

```shell
curl -L -o dockscan https://github.com/kardolus/dockscan/releases/download/v1.2/dockscan-darwin-amd64 && chmod +x dockscan && sudo mv dockscan /usr/local/bin/
```

### Linux (amd64)

```shell
curl -L -o dockscan https://github.com/kardolus/dockscan/releases/download/v1.2/dockscan-linux-amd64 && chmod +x dockscan && sudo mv dockscan /usr/local/bin/
```

### Linux (arm64)

```shell
curl -L -o dockscan https://github.com/kardolus/dockscan/releases/download/v1.2/dockscan-linux-arm64 && chmod +x dockscan && sudo mv dockscan /usr/local/bin/
```

### Windows (amd64)

Download the binary
from [this link](https://github.com/kardolus/dockscan/releases/download/v1.2/dockscan-windows-amd64.exe)
and add it to your PATH.

Choose the appropriate command for your system, which will download the binary, make it executable, and move it to your
/usr/local/bin directory (or %PATH% on Windows) for easy access.

## Usage

The `dockscan` CLI tool has several commands for interacting with the Citi Bike data:

### Basic information fetching

To fetch the current data and output it to your terminal in JSON format, run:

```shell
./bin/dockscan info
```

To better interpret the JSON output, you can use a tool like `jq`:

```shell
./bin/dockscan info | jq .
```

### Filtering by ID

You can filter the data to only show the status of certain stations by providing their IDs with the `--id` flag:

```shell
./bin/dockscan info --id 37a37e5b-f975-4f92-a897-dca8e4670631 --id c00ef46d-fcde-48e2-afbd-0fb595fe3fa7
```

### Time series data

You can collect time series data for a given station by using the `ts` command, providing the station's ID, and
specifying the interval (in seconds) at which data should be collected with the `--interval` flag:

For JSON:

```shell
./bin/dockscan ts --id 37a37e5b-f975-4f92-a897-dca8e4670631 --interval 300 
```

For CSV:

```shell
./bin/dockscan ts --id 37a37e5b-f975-4f92-a897-dca8e4670631 --interval 300 --csv
```

### Excluding columns

You can exclude certain columns from the output by providing their names with the --exclude flag:

```shell
./bin/dockscan ts --csv --exclude Longitude,Latitude,Location,ID
```

This command would produce output that excludes the 'Longitude', 'Latitude',  'Location' and 'ID' columns from each
station's data. The --exclude flag is case-insensitive, meaning --exclude Longitude would also work. You can use the
--exclude flag with any column names that appear in the output.

### Specify Output Directory

You can specify an output directory. This will create a CSV based on the current date (ie. 2023-07-23.csv) and put in
the current directory. When the date changes, so does the name of the CSV.

```shell
./bin/dockscan ts --csv --outdir /tmp
```

## Development

For developing the `dockscan` CLI tool, use the following steps to run tests and build the application:

1. Run the tests using the following scripts:

For unit tests, run:

```shell
./scripts/unit.sh
```

For integration tests, run:

```shell
./scripts/integration.sh
```

For contract tests, run:

```shell
./scripts/contract.sh
```

To run all tests, use:

```shell
./scripts/all-tests.sh
```

2. Build the app using the installation script:

```shell
./scripts/install.sh
```

3. After a successful build, test the application with the following command:

```shell
./bin/dockscan -h
```

## Uninstallation

If for any reason you wish to uninstall the `dockscan` CLI application from your system, you can do so by following
these steps:

### MacOS / Linux

If you installed the binary directly, remove it as such:

```shell
sudo rm /usr/local/bin/dockscan
```

### Windows

1. Navigate to the location of the `dockscan` binary in your system, which should be in your PATH.

2. Delete the `dockscan` binary.

## Contributing

We appreciate contributions to the dockscan-cli. Please feel free to submit issues and pull requests.