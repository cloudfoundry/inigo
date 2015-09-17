package helpers

import (
	"github.com/cloudfoundry-incubator/bbs"
	"github.com/cloudfoundry-incubator/bbs/models"
)
import . "github.com/onsi/gomega"

func ActiveActualLRPs(client bbs.Client, processGuid string) []models.ActualLRP {
	lrpGroups, err := client.ActualLRPGroupsByProcessGuid(processGuid)
	Expect(err).NotTo(HaveOccurred())

	startedLRPs := make([]models.ActualLRP, 0, len(lrpGroups))
	for _, lrpGroup := range lrpGroups {
		lrp, _ := lrpGroup.Resolve()
		if lrp.State != models.ActualLRPStateUnclaimed {
			startedLRPs = append(startedLRPs, *lrp)
		}
	}

	return startedLRPs
}

func TaskStatePoller(client bbs.Client, taskGuid string, task *models.Task) func() models.Task_State {
	return func() models.Task_State {
		rTask, err := client.TaskByGuid(taskGuid)
		Expect(err).NotTo(HaveOccurred())

		if task != nil {
			*task = *rTask
		}

		return rTask.State
	}
}

func LRPStatePoller(client bbs.Client, processGuid string, lrp *models.ActualLRP) func() string {
	return func() string {
		lrpGroups, err := client.ActualLRPGroupsByProcessGuid(processGuid)
		Expect(err).NotTo(HaveOccurred())

		if len(lrpGroups) == 0 {
			return ""
		}

		foundLRP, _ := lrpGroups[0].Resolve()

		if lrp != nil {
			*lrp = *foundLRP
		}

		return foundLRP.State
	}
}

func LRPInstanceStatePoller(client bbs.Client, processGuid string, index int, lrp *models.ActualLRP) func() string {
	return func() string {
		lrpGroup, err := client.ActualLRPGroupByProcessGuidAndIndex(processGuid, index)
		Expect(err).NotTo(HaveOccurred())

		foundLRP, _ := lrpGroup.Resolve()
		if lrp != nil {
			*lrp = *foundLRP
		}

		return foundLRP.State
	}
}
