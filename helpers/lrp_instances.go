package helpers

import (
	"encoding/json"
	"net/http"

	. "github.com/onsi/gomega"
	"github.com/tedsuo/rata"

	tpsapi "github.com/cloudfoundry-incubator/tps/api"
)

func RunningLRPInstancesPoller(tpsAddr string, guid string) func() []tpsapi.LRPInstance {
	return func() []tpsapi.LRPInstance {
		return RunningLRPInstances(tpsAddr, guid)
	}
}

func RunningLRPInstances(tpsAddr string, guid string) []tpsapi.LRPInstance {
	tpsRequestGenerator := rata.NewRequestGenerator("http://"+tpsAddr, tpsapi.Routes)

	getLRPs, err := tpsRequestGenerator.CreateRequest(
		tpsapi.LRPStatus,
		rata.Params{"guid": guid},
		nil,
	)
	Ω(err).ShouldNot(HaveOccurred())

	response, err := http.DefaultClient.Do(getLRPs)
	Ω(err).ShouldNot(HaveOccurred())
	defer response.Body.Close()

	var instances []tpsapi.LRPInstance
	err = json.NewDecoder(response.Body).Decode(&instances)
	Ω(err).ShouldNot(HaveOccurred())

	runningInstances := []tpsapi.LRPInstance{}
	for _, instance := range instances {
		if instance.State == "running" {
			runningInstances = append(runningInstances, instance)
		}
	}

	return runningInstances
}
