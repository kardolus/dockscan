package integration_test

import (
	"errors"
	"fmt"
	"github.com/kardolus/dockscan/client"
	"github.com/kardolus/dockscan/utils"
	"github.com/onsi/gomega/gexec"
	"io"
	"net/http"
	"sync"
)

var (
	onceBuild   sync.Once
	onceServe   sync.Once
	serverReady = make(chan struct{})
	binaryPath  string
)

func buildBinary() error {
	var err error
	onceBuild.Do(func() {
		binaryPath, err = gexec.Build(
			"github.com/kardolus/dockscan/cmd/dockscan",
			"-ldflags",
			fmt.Sprintf("-X main.GitCommit=%s -X main.GitVersion=%s -X main.ServiceURL=%s", gitCommit, gitVersion, serviceURL))
	})
	return err
}

func curl(url string) (string, error) {
	resp, err := http.Get(url)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	return string(data), nil
}

func runMockServer() error {
	var err error

	onceServe.Do(func() {
		go func() {
			http.HandleFunc("/ping", getPing)
			http.HandleFunc(client.StationInformationPath, getInformation)
			http.HandleFunc(client.StationStatusPath, getStatus)
			close(serverReady)
			err = http.ListenAndServe("", nil)
		}()
	})
	<-serverReady
	return err
}

func getPing(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	w.Write([]byte("pong"))
}

func getStatus(w http.ResponseWriter, r *http.Request) {
	if err := validateRequest(w, r, http.MethodGet); err != nil {
		fmt.Printf("invalid request: %s\n", err.Error())
		return
	}

	const statusFile = "station_status.json"
	response, err := utils.FileToBytes(statusFile)
	if err != nil {
		fmt.Printf("error reading %s: %s\n", statusFile, err.Error())
		return
	}
	w.Write(response)
}

func getInformation(w http.ResponseWriter, r *http.Request) {
	if err := validateRequest(w, r, http.MethodGet); err != nil {
		fmt.Printf("invalid request: %s\n", err.Error())
		return
	}

	const informationFile = "station_information.json"
	response, err := utils.FileToBytes(informationFile)
	if err != nil {
		fmt.Printf("error reading %s: %s\n", informationFile, err.Error())
		return
	}
	w.Write(response)
}

func validateRequest(w http.ResponseWriter, r *http.Request, allowedMethod string) error {
	if r.Method != allowedMethod {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return errors.New("method not allowed")
	}

	return nil
}
