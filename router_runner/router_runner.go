package router_runner

import (
	"fmt"
	"io/ioutil"
	"os"
	"os/exec"
	"time"

	"github.com/cloudfoundry/gorouter/config"
	"github.com/fraenkel/candiedyaml"
	"github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/onsi/gomega/gexec"
)

type Runner struct {
	routerPath string
	configFile *os.File

	session *gexec.Session

	*config.Config
}

func New(routerPath string, config *config.Config) *Runner {
	configFile, err := ioutil.TempFile(os.TempDir(), "router-config")
	Ω(err).ShouldNot(HaveOccurred())

	defer configFile.Close()

	runner := &Runner{
		routerPath: routerPath,
		configFile: configFile,

		Config: config,
	}

	err = candiedyaml.NewEncoder(configFile).Encode(runner.Config)
	Ω(err).ShouldNot(HaveOccurred())

	return runner
}

func (runner *Runner) Start() {
	sess, err := gexec.Start(
		exec.Command(runner.routerPath, "-c", runner.configFile.Name()),
		ginkgo.GinkgoWriter,
		ginkgo.GinkgoWriter,
	)

	Ω(err).ShouldNot(HaveOccurred())

	runner.session = sess
}

func (runner *Runner) Stop() {
	if runner.session != nil {
		runner.session.Kill().Wait(5 * time.Second)
	}
}

func (runner *Runner) Addr() string {
	return fmt.Sprintf("127.0.0.1:%d", runner.Config.Port)
}
