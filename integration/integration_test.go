package integration_test

import (
	"encoding/json"
	"fmt"
	"github.com/kardolus/dockscan/client"
	"github.com/kardolus/dockscan/types"
	. "github.com/onsi/gomega"
	"github.com/onsi/gomega/gexec"
	"github.com/sclevine/spec"
	"github.com/sclevine/spec/report"
	"io"
	"os/exec"
	"testing"
	"time"
)

const (
	gitCommit  = "some-git-commit"
	gitVersion = "some-git-version"
	serviceURL = "http://127.0.0.1"
)

func TestIntegration(t *testing.T) {
	defer gexec.CleanupBuildArtifacts()
	spec.Run(t, "Integration Tests", testIntegration, spec.Report(report.Terminal{}))
}

func testIntegration(t *testing.T, when spec.G, it spec.S) {
	it.Before(func() {
		RegisterTestingT(t)
	})

	when("Performing the Lifecycle", func() {
		const exitSuccess = 0

		it.Before(func() {
			SetDefaultEventuallyTimeout(5 * time.Second)

			Expect(buildBinary()).To(Succeed())
			Expect(runMockServer()).To(Succeed())

			Eventually(func() (string, error) {
				return curl(fmt.Sprintf("%s/ping", serviceURL))
			}).Should(ContainSubstring("pong"))
		})

		it.After(func() {
			gexec.Kill()
		})

		it("should return the expected result for the version command", func() {
			command := exec.Command(binaryPath, "version")
			session, err := gexec.Start(command, io.Discard, io.Discard)
			Expect(err).NotTo(HaveOccurred())

			Eventually(session).Should(gexec.Exit(exitSuccess))

			output := string(session.Out.Contents())
			Expect(output).To(ContainSubstring(fmt.Sprintf("commit %s", gitCommit)))
			Expect(output).To(ContainSubstring(fmt.Sprintf("version %s", gitVersion)))
		})

		it("should return the expected result for the info command", func() {
			command := exec.Command(binaryPath, "info")
			session, err := gexec.Start(command, io.Discard, io.Discard)
			Expect(err).NotTo(HaveOccurred())

			Eventually(session).Should(gexec.Exit(exitSuccess))

			output := string(session.Out.Contents())

			var result types.NormalizedStationData
			Expect(json.Unmarshal([]byte(output), &result)).To(Succeed())
			Expect(result.Stations).To(HaveLen(1964))

			Expect(result.Stations[3].ID).To(Equal("633fbc4c-7617-47ba-a393-aad7a8d26a3e"))
			Expect(result.Stations[3].Name).To(Equal("Van Brunt St & Van Dyke St"))
			Expect(result.Stations[3].Longitude).To(Equal(-74.01472628116608))
			Expect(result.Stations[3].Latitude).To(Equal(40.6758329439129))

			location := fmt.Sprintf(client.GoogleMapsQuery, 40.6758329439129, -74.01472628116608)
			Expect(result.Stations[3].Location).To(Equal(location))
			Expect(result.Stations[3].BikesAvailable).To(Equal(21))
			Expect(result.Stations[3].EBikesAvailable).To(Equal(4))
			Expect(result.Stations[3].BikesDisabled).To(Equal(3))
			Expect(result.Stations[3].DocksAvailable).To(Equal(2))
			Expect(result.Stations[3].DocksDisabled).To(Equal(7))
			Expect(result.Stations[3].IsReturning).To(BeTrue())
			Expect(result.Stations[3].IsRenting).To(BeFalse())
			Expect(result.Stations[3].IsInstalled).To(BeTrue())
		})
		it("should return the expected result when the --id flag is used on the info command", func() {
			command := exec.Command(binaryPath, "info", "--id", "37a37e5b-f975-4f92-a897-dca8e4670631", "--id", "c00ef46d-fcde-48e2-afbd-0fb595fe3fa7")
			session, err := gexec.Start(command, io.Discard, io.Discard)
			Expect(err).NotTo(HaveOccurred())

			Eventually(session).Should(gexec.Exit(exitSuccess))

			output := string(session.Out.Contents())

			var result types.NormalizedStationData
			Expect(json.Unmarshal([]byte(output), &result)).To(Succeed())
			Expect(result.Stations).To(HaveLen(2))

			Expect(result.Stations[0].ID).To(Equal("c00ef46d-fcde-48e2-afbd-0fb595fe3fa7"))
			Expect(result.Stations[1].ID).To(Equal("37a37e5b-f975-4f92-a897-dca8e4670631"))
		})
	})
}
