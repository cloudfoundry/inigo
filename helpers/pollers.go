package helpers

import (
	"code.cloudfoundry.org/bbs"
	"code.cloudfoundry.org/bbs/models"
	"code.cloudfoundry.org/lager"

	. "github.com/onsi/gomega"
)

func filteredActualLRPs(logger lager.Logger, client bbs.InternalClient, processGuid string, filter func(lrp *models.ActualLRP) bool) []models.ActualLRP {
	lrps, err := client.ActualLRPs(logger, models.ActualLRPFilter{ProcessGuid: processGuid})
	Expect(err).NotTo(HaveOccurred())

	startedLRPs := make([]models.ActualLRP, 0, len(lrps))
	for _, lrp := range lrps {
		if filter(lrp) {
			startedLRPs = append(startedLRPs, *lrp)
		}
	}

	return startedLRPs
}

func ActiveActualLRPs(logger lager.Logger, client bbs.InternalClient, processGuid string) []models.ActualLRP {
	return filteredActualLRPs(logger, client, processGuid, func(lrp *models.ActualLRP) bool {
		return lrp.State != models.ActualLRPStateUnclaimed
	})
}

func RunningActualLRPs(logger lager.Logger, client bbs.InternalClient, processGuid string) []models.ActualLRP {
	return filteredActualLRPs(logger, client, processGuid, func(lrp *models.ActualLRP) bool {
		return lrp.State == models.ActualLRPStateRunning
	})
}

func TaskStatePoller(logger lager.Logger, client bbs.InternalClient, taskGuid string, task *models.Task) func() models.Task_State {
	return func() models.Task_State {
		rTask, err := client.TaskByGuid(logger, taskGuid)
		Expect(err).NotTo(HaveOccurred())

		if task != nil {
			*task = *rTask
		}

		return rTask.State
	}
}

func TaskFailedPoller(logger lager.Logger, client bbs.InternalClient, taskGuid string, task *models.Task) func() bool {
	return func() bool {
		rTask, err := client.TaskByGuid(logger, taskGuid)
		Expect(err).NotTo(HaveOccurred())

		if task != nil {
			*task = *rTask
		}

		return rTask.Failed
	}
}

func LRPStatePoller(logger lager.Logger, client bbs.InternalClient, processGuid string, lrp *models.ActualLRP) func() string {
	return func() string {
		lrps, err := client.ActualLRPs(logger, models.ActualLRPFilter{ProcessGuid: processGuid})
		Expect(err).NotTo(HaveOccurred())
		if len(lrps) == 0 {
			return ""
		}
		Expect(len(lrps)).To(BeNumerically(">", 0))
		if lrp != nil {
			*lrp = *lrps[0]
		}
		return lrps[0].State
	}
}

func LRPInstanceStatePoller(logger lager.Logger, client bbs.InternalClient, processGuid string, index int, lrp *models.ActualLRP) func() string {
	return func() string {
		i := int32(index)
		lrps, err := client.ActualLRPs(logger, models.ActualLRPFilter{ProcessGuid: processGuid, Index: &i})
		Expect(err).NotTo(HaveOccurred())
		Expect(len(lrps)).To(Equal(1))
		if lrp != nil {
			*lrp = *lrps[0]
		}
		return lrps[0].State
	}
}
