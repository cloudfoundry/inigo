package helpers

import (
	"encoding/json"
	"net/http"

	. "github.com/onsi/gomega"
	"github.com/tedsuo/rata"

	"github.com/cloudfoundry-incubator/runtime-schema/cc_messages"
	"github.com/cloudfoundry-incubator/tps"
)

func RunningLRPInstancesPoller(tpsAddr string, guid string) func() []cc_messages.LRPInstance {
	return func() []cc_messages.LRPInstance {
		return RunningLRPInstances(tpsAddr, guid)
	}
}

func RunningLRPInstances(tpsAddr string, guid string) []cc_messages.LRPInstance {
	instances := GetLRPInstances(tpsAddr, guid)

	runningInstances := []cc_messages.LRPInstance{}
	for _, instance := range instances {
		if instance.State == cc_messages.LRPInstanceStateRunning {
			runningInstances = append(runningInstances, instance)
		}
	}

	return runningInstances
}

func GetLRPInstancesPoller(tpsAddr string, guid string) func() []cc_messages.LRPInstance {
	return func() []cc_messages.LRPInstance {
		return GetLRPInstances(tpsAddr, guid)
	}
}

func GetLRPInstances(tpsAddr string, guid string) []cc_messages.LRPInstance {
	tpsRequestGenerator := rata.NewRequestGenerator("http://"+tpsAddr, tps.Routes)

	getLRPs, err := tpsRequestGenerator.CreateRequest(
		tps.LRPStatus,
		rata.Params{"guid": guid},
		nil,
	)
	Expect(err).NotTo(HaveOccurred())

	response, err := http.DefaultClient.Do(getLRPs)
	Expect(err).NotTo(HaveOccurred())
	defer response.Body.Close()

	var instances []cc_messages.LRPInstance
	err = json.NewDecoder(response.Body).Decode(&instances)
	Expect(err).NotTo(HaveOccurred())

	return instances
}
