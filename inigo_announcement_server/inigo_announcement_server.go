package inigo_announcement_server

import (
	"encoding/json"
	"fmt"
	"sync"

	"net/http"
	"net/http/httptest"

	garden_api "github.com/cloudfoundry-incubator/garden/api"
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
		case "/announce":
			lock.Lock()
			registered = append(registered, r.URL.Query().Get("announcement"))
			lock.Unlock()
		case "/announcements":
			lock.RLock()
			json.NewEncoder(w).Encode(registered)
			lock.RUnlock()
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	})
}

func Stop(gardenClient garden_api.Client) {
	server.Close()
}

func AnnounceURL(announcement string) string {
	return fmt.Sprintf("http://%s/announce?announcement=%s", serverAddr, announcement)
}

func Announcements() []string {
	response, err := http.Get(fmt.Sprintf("http://%s/announcements", serverAddr))
	Ω(err).ShouldNot(HaveOccurred())

	defer response.Body.Close()

	var responses []string

	err = json.NewDecoder(response.Body).Decode(&responses)
	Ω(err).ShouldNot(HaveOccurred())

	return responses
}
