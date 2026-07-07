package integration_test

import (
	"encoding/json"
	"github.com/kardolus/dockscan/client"
	"github.com/kardolus/dockscan/http"
	"github.com/kardolus/dockscan/types"
	. "github.com/onsi/gomega"
	"github.com/sclevine/spec"
	"github.com/sclevine/spec/report"
	"testing"
)

func TestContract(t *testing.T) {
	spec.Run(t, "Contract Tests", testContract, spec.Report(report.Terminal{}))
}

func testContract(t *testing.T, when spec.G, it spec.S) {
	var restCaller *http.RestCaller

	it.Before(func() {
		RegisterTestingT(t)

		restCaller = http.New()
	})

	when("accessing the station-status endpoint", func() {
		it("should have the expected keys in the response", func() {
			resp, err := restCaller.Get(client.DefaultServiceURL + client.StationStatusPath)
			Expect(err).NotTo(HaveOccurred())

			var data types.StationStatus
			err = json.Unmarshal(resp, &data)
			Expect(err).NotTo(HaveOccurred())

			Expect(data.LastUpdated).Should(BeNumerically(">", 0), "Expected LastUpdated to be present and greater than zero in the response")
			Expect(data.TTL).Should(BeNumerically(">", 0), "Expected TTL to be present and greater than zero in the response")

			Expect(data.Data.Stations).NotTo(BeEmpty())

			station := data.Data.Stations[0]

			Expect(station.NumScootersUnavailable).Should(BeNumerically(">=", 0), "Expected NumScootersUnavailable to be present and greater or equal to zero in the response")
			Expect(station.LastReported).Should(BeNumerically(">", 0), "Expected LastReported to be present and greater than zero in the response")
			Expect(station.IsReturning).Should(BeNumerically(">=", 0), "Expected IsReturning to be present and greater or equal to zero in the response")
			Expect(station.StationID).ShouldNot(BeEmpty(), "Expected StationID to be present in the response")
			Expect(station.NumEbikesAvailable).Should(BeNumerically(">=", 0), "Expected NumEbikesAvailable to be present and greater or equal to zero in the response")
			Expect(station.NumScootersAvailable).Should(BeNumerically(">=", 0), "Expected NumScootersAvailable to be present and greater or equal to zero in the response")
			Expect(station.IsRenting).Should(BeNumerically(">=", 0), "Expected IsRenting to be present and greater or equal to zero in the response")
			Expect(station.NumBikesDisabled).Should(BeNumerically(">=", 0), "Expected NumBikesDisabled to be present and greater or equal to zero in the response")
			Expect(station.IsInstalled).Should(BeNumerically(">=", 0), "Expected IsInstalled to be present and greater or equal to zero in the response")
			Expect(station.NumDocksDisabled).Should(BeNumerically(">=", 0), "Expected NumDocksDisabled to be present and greater or equal to zero in the response")
			Expect(station.NumBikesAvailable).Should(BeNumerically(">=", 0), "Expected NumBikesAvailable to be present and greater or equal to zero in the response")
			Expect(station.NumDocksAvailable).Should(BeNumerically(">=", 0), "Expected NumDocksAvailable to be present and greater or equal to zero in the response")
		})
	})

	when("accessing the station-information endpoint", func() {
		it("should have the expected keys in the response", func() {
			resp, err := restCaller.Get(client.DefaultServiceURL + client.StationInformationPath)
			Expect(err).NotTo(HaveOccurred())

			var data types.StationInformation
			err = json.Unmarshal(resp, &data)
			Expect(err).NotTo(HaveOccurred())

			Expect(data.LastUpdated).Should(BeNumerically(">", 0), "Expected LastUpdated to be present and greater than zero in the response")
			Expect(data.TTL).Should(BeNumerically(">", 0), "Expected TTL to be present and greater than zero in the response")

			Expect(data.Data.Stations).NotTo(BeEmpty())

			station := data.Data.Stations[0]

			Expect(station.ExternalID).ShouldNot(BeEmpty(), "Expected ExternalID to be present in the response")
			Expect(station.Lat).Should(BeNumerically(">=", -90), "Expected Lat to be present and within range -90.0 to 90.0 in the response")
			Expect(station.Lat).Should(BeNumerically("<=", 90), "Expected Lat to be present and within range -90.0 to 90.0 in the response")
			Expect(station.StationID).ShouldNot(BeEmpty(), "Expected StationID to be present in the response")
			Expect(station.RentalUris.Ios).ShouldNot(BeEmpty(), "Expected RentalUris.Ios to be present in the response")
			Expect(station.RentalUris.Android).ShouldNot(BeEmpty(), "Expected RentalUris.Android to be present in the response")
			Expect(station.Lon).Should(BeNumerically(">=", -180), "Expected Lon to be present and within range -180.0 to 180.0 in the response")
			Expect(station.Lon).Should(BeNumerically("<=", 180), "Expected Lon to be present and within range -180.0 to 180.0 in the response")
			Expect(station.StationType).ShouldNot(BeEmpty(), "Expected StationType to be present in the response")
			Expect(station.Capacity).Should(BeNumerically(">=", 0), "Expected Capacity to be present and greater or equal to zero in the response")
			Expect(station.Name).ShouldNot(BeEmpty(), "Expected Name to be present in the response")
			Expect(station.ShortName).ShouldNot(BeEmpty(), "Expected ShortName to be present in the response")
			Expect(station.RentalMethods).ShouldNot(BeEmpty(), "Expected RentalMethods to be present in the response")
		})
	})
}
