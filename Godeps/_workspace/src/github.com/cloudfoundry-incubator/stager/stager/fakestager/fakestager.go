package fakestager

import (
	"errors"
	"github.com/cloudfoundry-incubator/stager/stager"
)

type FakeStager struct {
	TimesStageInvoked int
	StagingRequests   []stager.StagingRequest
	AlwaysFail        bool //bringing shame and disgrace to its family and friends
}

func (stager *FakeStager) Stage(stagingRequest stager.StagingRequest, replyTo string) error {
	stager.TimesStageInvoked++
	stager.StagingRequests = append(stager.StagingRequests, stagingRequest)

	if stager.AlwaysFail {
		return errors.New("The thingy broke :(")
	} else {
		return nil
	}
}
