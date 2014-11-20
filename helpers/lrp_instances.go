package helpers

import (
	"encoding/json"
	"net/http"

	. "github.com/onsi/gomega"
	"github.com/tedsuo/rata"

	"github.com/cloudfoundry-incubator/tps"
)

func RunningLRPInstancesPoller(tpsAddr string, guid string) func() []tps.LRPInstance {
	return func() []tps.LRPInstance {
		return RunningLRPInstances(tpsAddr, guid)
	}
}

func RunningLRPInstances(tpsAddr string, guid string) []tps.LRPInstance {
	instances := GetLRPInstances(tpsAddr, guid)

	runningInstances := []tps.LRPInstance{}
	for _, instance := range instances {
		if instance.State == "running" {
			runningInstances = append(runningInstances, instance)
		}
	}

	return runningInstances
}

func GetLRPInstancesPoller(tpsAddr string, guid string) func() []tps.LRPInstance {
	return func() []tps.LRPInstance {
		return GetLRPInstances(tpsAddr, guid)
	}
}

func GetLRPInstances(tpsAddr string, guid string) []tps.LRPInstance {
	tpsRequestGenerator := rata.NewRequestGenerator("http://"+tpsAddr, tps.Routes)

	getLRPs, err := tpsRequestGenerator.CreateRequest(
		tps.LRPStatus,
		rata.Params{"guid": guid},
		nil,
	)
	Ω(err).ShouldNot(HaveOccurred())

	response, err := http.DefaultClient.Do(getLRPs)
	Ω(err).ShouldNot(HaveOccurred())
	defer response.Body.Close()

	var instances []tps.LRPInstance
	err = json.NewDecoder(response.Body).Decode(&instances)
	Ω(err).ShouldNot(HaveOccurred())

	return instances
}
