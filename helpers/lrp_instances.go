package helpers

import (
	"encoding/json"
	. "github.com/onsi/gomega"
	"github.com/tedsuo/router"
	"net/http"

	tpsapi "github.com/cloudfoundry-incubator/tps/api"
)

func RunningLRPInstancesPoller(tpsAddr string, guid string) func() []tpsapi.LRPInstance {
	return func() []tpsapi.LRPInstance {
		return RunningLRPInstances(tpsAddr, guid)
	}
}

func RunningLRPInstances(tpsAddr string, guid string) []tpsapi.LRPInstance {
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

	defer response.Body.Close()
	err = json.NewDecoder(response.Body).Decode(&instances)
	Ω(err).ShouldNot(HaveOccurred())

	instancesToReturn := []tpsapi.LRPInstance{}
	for _, instance := range instances {
		if instance.State == "running" {
			instancesToReturn = append(instancesToReturn, instance)
		}
	}

	return instancesToReturn
}
