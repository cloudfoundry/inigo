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
