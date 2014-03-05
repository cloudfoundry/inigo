package fake_cc

import (
	"fmt"
	"github.com/cloudfoundry/gunk/test_server"
	"github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"io/ioutil"
	"net/http"
	"net/http/httptest"
	"regexp"
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
	UploadedDroplets map[string][]byte
	server           *httptest.Server
}

func New() *FakeCC {
	return &FakeCC{
		UploadedDroplets: map[string][]byte{},
	}
}

func (f *FakeCC) Start() (address string) {
	f.server = httptest.NewServer(f)

	return f.server.URL
}

func (f *FakeCC) Reset() {
	f.UploadedDroplets = map[string][]byte{}
}

func (f *FakeCC) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	fmt.Fprintf(ginkgo.GinkgoWriter, "[FAKE CC] Handling request: %s\n", r.URL.Path)
	Ω(r.URL.Path).Should(MatchRegexp("/staging/droplets/.*/upload"))

	basicAuthVerifier := test_server.VerifyBasicAuth(CC_USERNAME, CC_PASSWORD)
	basicAuthVerifier(w, r)

	file, _, err := r.FormFile("upload[droplet]")
	Ω(err).ShouldNot(HaveOccurred())

	uploadedBytes, err := ioutil.ReadAll(file)
	Ω(err).ShouldNot(HaveOccurred())

	re := regexp.MustCompile("/staging/droplets/(.*)/upload")
	appGuid := re.FindStringSubmatch(r.URL.Path)[1]

	f.UploadedDroplets[appGuid] = uploadedBytes
	fmt.Fprintf(ginkgo.GinkgoWriter, "[FAKE CC] Got %d bytes for droplet for app-guid %s\n", len(uploadedBytes), appGuid)

	w.Write([]byte(finishedResponseBody))
}
