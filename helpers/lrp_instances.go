package helpers

import (
	"encoding/json"
	"net/http"
	. "github.com/onsi/gomega"

	tpsapi "github.com/cloudfoundry-incubator/tps/api"
	"github.com/tedsuo/router"
)

func LRPInstances(tpsAddr string, guid string) []tpsapi.LRPInstance {
	tpsRequestGenerator := router.NewRequestGenerator(tpsAddr, tpsapi.Routes)

	getLRPs, err := tpsRequestGenerator.RequestForHandler(
		tpsapi.LRPStatus,
		router.Params{"guid": guid},
		nil,
	)
	Ω(err).ShouldNot(HaveOccurred())

	var instances []tpsapi.LRPInstance
	response, err := http.DefaultClient.Do(getLRPs)
	Ω(err).ShouldNot(HaveOccurred())

	err = json.NewDecoder(response.Body).Decode(&instances)
	Ω(err).ShouldNot(HaveOccurred())

	return instances
}
