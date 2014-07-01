package fake_cc

import (
	"bytes"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"os"
	"regexp"
	"strconv"

	"github.com/cloudfoundry/gunk/test_server"
	"github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/tedsuo/ifrit/http_server"
)

const (
	CC_USERNAME          = "bob"
	CC_PASSWORD          = "password"
	finishedResponseBody = `
        {
            "metadata":{
                "guid": "inigo-job-guid",
                "url": "/v2/jobs/inigo-job-guid"
            },
            "entity": {
                "status": "finished"
            }
        }
    `
)

type FakeCC struct {
	address string

	UploadedDroplets             map[string][]byte
	UploadedBuildArtifactsCaches map[string][]byte
}

func New(address string) *FakeCC {
	return &FakeCC{
		address: address,

		UploadedDroplets:             map[string][]byte{},
		UploadedBuildArtifactsCaches: map[string][]byte{},
	}
}

func (f *FakeCC) Run(signals <-chan os.Signal, ready chan<- struct{}) error {
	err := http_server.New(f.address, f).Run(signals, ready)

	f.Reset()

	return err
}

func (f *FakeCC) Username() string {
	return CC_USERNAME
}

func (f *FakeCC) Password() string {
	return CC_PASSWORD
}

func (f *FakeCC) Reset() {
	f.UploadedDroplets = map[string][]byte{}
	f.UploadedBuildArtifactsCaches = map[string][]byte{}
}

func (f *FakeCC) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	fmt.Fprintf(ginkgo.GinkgoWriter, "[FAKE CC] Handling request: %s\n", r.URL.Path)

	endpoints := map[string]func(http.ResponseWriter, *http.Request){
		"/staging/droplets/.*/upload":          f.handleDropletUploadRequest,
		"/staging/buildpack_cache/.*/upload":   f.handleBuildArtifactsCacheUploadRequest,
		"/staging/buildpack_cache/.*/download": f.handleBuildArtifactsCacheDownloadRequest,
	}

	for pattern, handler := range endpoints {
		re := regexp.MustCompile(pattern)
		matches := re.FindStringSubmatch(r.URL.Path)
		if matches != nil {
			handler(w, r)
			return
		}
	}

	ginkgo.Fail(fmt.Sprintf("[FAKE CC] No matching endpoint handler for %s", r.URL.Path))
}

func (f *FakeCC) handleDropletUploadRequest(w http.ResponseWriter, r *http.Request) {
	basicAuthVerifier := test_server.VerifyBasicAuth(CC_USERNAME, CC_PASSWORD)
	basicAuthVerifier(w, r)

	file, _, err := r.FormFile("upload[droplet]")
	Ω(err).ShouldNot(HaveOccurred())

	uploadedBytes, err := ioutil.ReadAll(file)
	Ω(err).ShouldNot(HaveOccurred())

	re := regexp.MustCompile("/staging/droplets/(.*)/upload")
	appGuid := re.FindStringSubmatch(r.URL.Path)[1]

	f.UploadedDroplets[appGuid] = uploadedBytes
	fmt.Fprintf(ginkgo.GinkgoWriter, "[FAKE CC] Received %d bytes for droplet for app-guid %s\n", len(uploadedBytes), appGuid)

	w.WriteHeader(http.StatusOK)
	w.Write([]byte(finishedResponseBody))
}

func (f *FakeCC) handleBuildArtifactsCacheUploadRequest(w http.ResponseWriter, r *http.Request) {
	basicAuthVerifier := test_server.VerifyBasicAuth(CC_USERNAME, CC_PASSWORD)
	basicAuthVerifier(w, r)

	file, _, err := r.FormFile("upload[droplet]")
	Ω(err).ShouldNot(HaveOccurred())

	uploadedBytes, err := ioutil.ReadAll(file)
	Ω(err).ShouldNot(HaveOccurred())

	re := regexp.MustCompile("/staging/buildpack_cache/(.*)/upload")
	appGuid := re.FindStringSubmatch(r.URL.Path)[1]

	f.UploadedBuildArtifactsCaches[appGuid] = uploadedBytes
	fmt.Fprintf(ginkgo.GinkgoWriter, "[FAKE CC] Received %d bytes for build artifacts cache for app-guid %s\n", len(uploadedBytes), appGuid)

	w.WriteHeader(http.StatusOK)
}

func (f *FakeCC) handleBuildArtifactsCacheDownloadRequest(w http.ResponseWriter, r *http.Request) {
	basicAuthVerifier := test_server.VerifyBasicAuth(CC_USERNAME, CC_PASSWORD)
	basicAuthVerifier(w, r)

	re := regexp.MustCompile("/staging/buildpack_cache/(.*)/download")
	appGuid := re.FindStringSubmatch(r.URL.Path)[1]

	fmt.Fprintf(ginkgo.GinkgoWriter, "[FAKE CC] Received request to download build artifacts cache for app-guid %s\n", appGuid)

	buildArtifactsCache := f.UploadedBuildArtifactsCaches[appGuid]
	if buildArtifactsCache == nil {
		fmt.Fprintf(ginkgo.GinkgoWriter, "[FAKE CC] No matching build artifacts cache for app-guid %s\n", appGuid)

		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte("File Not Found"))
		return
	}

	w.WriteHeader(http.StatusOK)

	contentLength := len(buildArtifactsCache)
	w.Header().Set("Content-Length", strconv.Itoa(contentLength))
	fmt.Fprintf(ginkgo.GinkgoWriter, "[FAKE CC] Responding with build artifacts cache for app-guid %s. Content-Length: %d\n", appGuid, contentLength)

	buffer := bytes.NewBuffer(buildArtifactsCache)
	io.Copy(w, buffer)
}
