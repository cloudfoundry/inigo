package helpers

import "github.com/cloudfoundry-incubator/receptor"
import . "github.com/onsi/gomega"

func ActiveActualLRPs(receptorClient receptor.Client, processGuid string) []receptor.ActualLRPResponse {
	lrps, err := receptorClient.ActualLRPsByProcessGuid(processGuid)
	Expect(err).NotTo(HaveOccurred())

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
		Expect(err).NotTo(HaveOccurred())

		*task = rTask

		return task.State
	}
}

func LRPStatePoller(receptorClient receptor.Client, processGuid string, lrp *receptor.ActualLRPResponse) func() receptor.ActualLRPState {
	return func() receptor.ActualLRPState {
		lrps, err := receptorClient.ActualLRPsByProcessGuid(processGuid)
		Expect(err).NotTo(HaveOccurred())

		if len(lrps) == 0 {
			return receptor.ActualLRPStateInvalid
		}

		if lrp != nil {
			*lrp = lrps[0]
		}

		return lrps[0].State
	}
}

func LRPInstanceStatePoller(receptorClient receptor.Client, processGuid string, index int, lrp *receptor.ActualLRPResponse) func() receptor.ActualLRPState {
	return func() receptor.ActualLRPState {
		lrpInstance, err := receptorClient.ActualLRPByProcessGuidAndIndex(processGuid, index)
		Expect(err).NotTo(HaveOccurred())

		if lrp != nil {
			*lrp = lrpInstance
		}

		return lrpInstance.State
	}
}
