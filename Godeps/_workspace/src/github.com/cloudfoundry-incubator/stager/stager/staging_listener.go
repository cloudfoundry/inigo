package stager

import (
	"encoding/json"
	steno "github.com/cloudfoundry/gosteno"
	"github.com/cloudfoundry/yagnats"
)

type StagingListener struct {
	natsClient yagnats.NATSClient
	logger     *steno.Logger
	stager     Stager
}

func Listen(natsClient yagnats.NATSClient, stager Stager, logger *steno.Logger) error {
	stagingListener := StagingListener{
		natsClient: natsClient,
		logger:     logger,
		stager:     stager,
	}

	return stagingListener.Listen()
}

func (stagingListener *StagingListener) Listen() error {
	_, err := stagingListener.natsClient.SubscribeWithQueue("diego.staging.start", "diego.stagers", func(message *yagnats.Message) {
		startMessage := StagingRequest{}

		err := json.Unmarshal(message.Payload, &startMessage)
		if err != nil {
			stagingListener.logError("staging.request.malformed", err, message)
			stagingListener.sendErrorResponse(message.ReplyTo, "Staging message contained invalid JSON")
			return
		}

		stagingListener.logger.Infod(
			map[string]interface{}{
				"message": startMessage,
			},
			"staging.request.received",
		)

		err = stagingListener.stager.Stage(startMessage, message.ReplyTo)
		if err != nil {
			stagingListener.logError("staging.request.failed", err, startMessage)
			stagingListener.sendErrorResponse(message.ReplyTo, "Staging failed")
			return
		}

		stagingListener.logger.Infod(
			map[string]interface{}{
				"message": startMessage,
			},
			"staging.request.succeeded",
		)

		response := StagingResponse{}
		if responseJson, err := json.Marshal(response); err == nil {
			stagingListener.natsClient.Publish(message.ReplyTo, responseJson)
		}
	})

	return err
}

func (stagingListener *StagingListener) logError(logMessage string, err error, message interface{}) {
	stagingListener.logger.Errord(map[string]interface{}{
		"message": message,
		"error":   err,
	}, logMessage)
}

func (stagingListener *StagingListener) sendErrorResponse(replyTo string, errorMessage string) {
	response := StagingResponse{Error: errorMessage}
	if responseJson, err := json.Marshal(response); err == nil {
		stagingListener.natsClient.Publish(replyTo, responseJson)
	}
}
