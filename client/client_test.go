package client_test

import (
	"fmt"
	"github.com/golang/mock/gomock"
	_ "github.com/golang/mock/mockgen/model"
	"github.com/kardolus/dockscan/client"
	"github.com/kardolus/dockscan/utils"
	. "github.com/onsi/gomega"
	"github.com/sclevine/spec"
	"github.com/sclevine/spec/report"
	"testing"
	"time"
)

//go:generate mockgen -destination=callermocks_test.go -package=client_test github.com/kardolus/dockscan/http Caller
//go:generate mockgen -destination=timemocks_test.go -package=client_test github.com/kardolus/dockscan/client TimeProvider

var (
	mockCtrl         *gomock.Controller
	mockCaller       *MockCaller
	mockTimeProvider *MockTimeProvider
	subject          *client.Client
)

func TestUnitClient(t *testing.T) {
	spec.Run(t, "Testing the client package", testClient, spec.Report(report.Terminal{}))
}

func testClient(t *testing.T, when spec.G, it spec.S) {
	it.Before(func() {
		RegisterTestingT(t)
		mockCtrl = gomock.NewController(t)
		mockCaller = NewMockCaller(mockCtrl)
		mockTimeProvider = NewMockTimeProvider(mockCtrl)
	})

	it.After(func() {
		mockCtrl.Finish()
	})

	when("New()", func() {
		it("throws an error when it fails to decode the response", func() {
			malformed := `{"invalid":"json"` // missing closing brace
			mockCaller.EXPECT().Get(client.DefaultServiceURL+client.StationInformationPath).Return([]byte(malformed), nil)

			builder := client.NewClientBuilder().WithTimeProvider(mockTimeProvider).WithCaller(mockCaller)
			_, err := builder.Build()

			Expect(err).To(HaveOccurred())
			Expect(err.Error()).Should(HavePrefix("failed to decode response:"))
		})
		it("throws an error when the response is empty", func() {
			mockCaller.EXPECT().Get(client.DefaultServiceURL+client.StationInformationPath).Return(nil, nil)

			builder := client.NewClientBuilder().WithTimeProvider(mockTimeProvider).WithCaller(mockCaller)
			_, err := builder.Build()

			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(Equal("empty response"))
		})
		it("applies the expected ID filter", func() {
			response, err := utils.FileToBytes("station_information.json")
			Expect(err).NotTo(HaveOccurred())

			mockCaller.EXPECT().Get(client.DefaultServiceURL+client.StationInformationPath).Return(response, nil).Times(1)

			ids := []string{"c00ef46d-fcde-48e2-afbd-0fb595fe3fa7", "37a37e5b-f975-4f92-a897-dca8e4670631"}

			now := time.Now()
			mockTimeProvider.EXPECT().Now().Return(now).Times(1)

			builder := client.NewClientBuilder().WithIDFilter(ids).WithTimeProvider(mockTimeProvider).WithCaller(mockCaller)
			subject, err = builder.Build()

			Expect(err).NotTo(HaveOccurred())

			response, err = utils.FileToBytes("station_status.json")
			Expect(err).NotTo(HaveOccurred())

			mockCaller.EXPECT().Get(client.DefaultServiceURL+client.StationStatusPath).Return(response, nil).Times(1)

			mockTimeProvider.EXPECT().Now().Return(now).Times(1)

			result, err := subject.ParseStationData()
			Expect(err).NotTo(HaveOccurred())

			Expect(result.Stations).To(HaveLen(2))
			Expect(result.Stations[0].ID).To(Equal("c00ef46d-fcde-48e2-afbd-0fb595fe3fa7"))
			Expect(result.Stations[1].ID).To(Equal("37a37e5b-f975-4f92-a897-dca8e4670631"))
		})
	})

	when("ParseStationData()", func() {
		it.Before(func() {
			response, err := utils.FileToBytes("station_information.json")
			Expect(err).NotTo(HaveOccurred())

			mockCaller.EXPECT().Get(client.DefaultServiceURL+client.StationInformationPath).Return(response, nil).Times(1)

			now := time.Now()
			mockTimeProvider.EXPECT().Now().Return(now).Times(1)

			builder := client.NewClientBuilder().WithTimeProvider(mockTimeProvider).WithCaller(mockCaller)
			subject, err = builder.Build()

			Expect(err).NotTo(HaveOccurred())
		})

		it("throws an error when it fails to decode the response", func() {
			malformed := `{"invalid":"json"` // missing closing brace
			mockCaller.EXPECT().Get(client.DefaultServiceURL+client.StationStatusPath).Return([]byte(malformed), nil)

			_, err := subject.ParseStationData()
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).Should(HavePrefix("failed to decode response:"))
		})
		it("throws an error when the response is empty", func() {
			mockCaller.EXPECT().Get(client.DefaultServiceURL+client.StationStatusPath).Return(nil, nil)

			_, err := subject.ParseStationData()
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(Equal("empty response"))
		})
		it("parses the output as expected", func() {
			response, err := utils.FileToBytes("station_status.json")
			Expect(err).NotTo(HaveOccurred())

			mockCaller.EXPECT().Get(client.DefaultServiceURL+client.StationStatusPath).Return(response, nil).Times(1)

			now := time.Now()
			mockTimeProvider.EXPECT().Now().Return(now).Times(1)

			result, err := subject.ParseStationData()
			Expect(err).NotTo(HaveOccurred())

			Expect(result.TimeStamp).To(Equal(now))
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
	})
}
