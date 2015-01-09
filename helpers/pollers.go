package helpers

import "github.com/cloudfoundry-incubator/receptor"
import . "github.com/onsi/gomega"

func ActiveActualLRPs(receptorClient receptor.Client, processGuid string) []receptor.ActualLRPResponse {
	lrps, err := receptorClient.ActualLRPsByProcessGuid(processGuid)
	Ω(err).ShouldNot(HaveOccurred())

	startedLRPs := make([]receptor.ActualLRPResponse, 0, len(lrps))
	for _, l := range lrps {
		if l.State != receptor.ActualLRPStateUnclaimed {
			startedLRPs = append(startedLRPs, l)
		}
	}

	return startedLRPs
}

func TaskStatePoller(receptorClient receptor.Client, taskGuid string, task *receptor.TaskResponse) func() string {
	return func() string {
		rTask, err := receptorClient.GetTask(taskGuid)
		Ω(err).ShouldNot(HaveOccurred())

		*task = rTask

		return task.State
	}
}

func LRPStatePoller(receptorClient receptor.Client, processGuid string, lrp *receptor.ActualLRPResponse) func() receptor.ActualLRPState {
	return func() receptor.ActualLRPState {
		lrps, err := receptorClient.ActualLRPsByProcessGuid(processGuid)
		Ω(err).ShouldNot(HaveOccurred())

		if len(lrps) == 0 {
			return receptor.ActualLRPStateInvalid
		}

		*lrp = lrps[0]

		return lrp.State
	}
}
