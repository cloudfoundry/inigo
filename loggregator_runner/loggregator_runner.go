package loggregator_runner

import (
	"encoding/json"
	"io/ioutil"
	"os"
	"os/exec"
	"time"

	"github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/onsi/gomega/gexec"
)

type LoggregatorRunner struct {
	loggregatorPath string
	configFile      *os.File

	session *gexec.Session

	Config
}

type Config struct {
	IncomingPort           int
	OutgoingPort           int
	MaxRetainedLogMessages int
	SharedSecret           string

	NatsHost string
	NatsPort int
}

func New(loggregatorPath string, config Config) *LoggregatorRunner {
	configFile, err := ioutil.TempFile(os.TempDir(), "loggregator-config")
	Ω(err).ShouldNot(HaveOccurred())

	defer configFile.Close()

	runner := &LoggregatorRunner{
		loggregatorPath: loggregatorPath,
		configFile:      configFile,

		Config: config,
	}

	err = json.NewEncoder(configFile).Encode(runner.Config)
	Ω(err).ShouldNot(HaveOccurred())

	return runner
}

func (runner *LoggregatorRunner) Start() {
	sess, err := gexec.Start(
		exec.Command(runner.loggregatorPath, "--config", runner.configFile.Name()),
		ginkgo.GinkgoWriter,
		ginkgo.GinkgoWriter,
	)

	Ω(err).ShouldNot(HaveOccurred())

	runner.session = sess
}

func (runner *LoggregatorRunner) Stop() {
	if runner.session != nil {
		runner.session.Kill().Wait(5 * time.Second)
	}
}
