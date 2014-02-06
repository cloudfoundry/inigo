package stager

import (
	steno "github.com/cloudfoundry/gosteno"
	"os"
)

var logger *steno.Logger

func init() {
	steno.Init(&steno.Config{
		Sinks: []steno.Sink{
			steno.NewIOSink(os.Stdout),
		},
	})

	steno.NewLogger("Stager")
}
