package inigo_server

import (
	"encoding/json"
	"fmt"
	"sync"

	"net/http"
	"net/http/httptest"

	"github.com/cloudfoundry-incubator/garden/warden"
	"github.com/cloudfoundry-incubator/inigo/helpers"
	. "github.com/onsi/gomega"
)

var server *httptest.Server
var serverAddr string

func Start(externalAddress string) {
	lock := &sync.RWMutex{}

	registered := []string{}

	server, serverAddr = helpers.Callback(externalAddress, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/register":
			lock.Lock()
			registered = append(registered, r.URL.Query().Get("guid"))
			lock.Unlock()
		case "/registrations":
			lock.RLock()
			json.NewEncoder(w).Encode(registered)
			lock.RUnlock()
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	})
}

func Stop(wardenClient warden.Client) {
	server.Close()
}

func CurlArgs(guid string) []string {
	return []string{fmt.Sprintf("http://%s/register?guid=%s", serverAddr, guid)}
}

func ReportingGuids() []string {
	response, err := http.Get(fmt.Sprintf("http://%s/registrations", serverAddr))
	Ω(err).ShouldNot(HaveOccurred())

	defer response.Body.Close()

	var responses []string

	err = json.NewDecoder(response.Body).Decode(&responses)
	Ω(err).ShouldNot(HaveOccurred())

	return responses
}
