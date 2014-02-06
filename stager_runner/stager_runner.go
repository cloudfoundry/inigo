package stager_runner

import (
	"os/exec"
	"strings"
	"syscall"
	"time"

	"github.com/cloudfoundry-incubator/inigo/runner_support"
	. "github.com/onsi/gomega"
	"github.com/vito/cmdtest"
	. "github.com/vito/cmdtest/matchers"
)

type StagerRunner struct {
	stagerBin    string
	etcdMachines []string

	stagerSession *cmdtest.Session
}

func New(stagerBin string, etcdMachines []string) *StagerRunner {
	return &StagerRunner{
		stagerBin:    stagerBin,
		etcdMachines: etcdMachines,
	}
}

func (r *StagerRunner) Start() {
	stagerSession, err := cmdtest.StartWrapped(
		exec.Command(
			r.stagerBin,
			"-etcdMachines", strings.Join(r.etcdMachines, ","),
		),
		runner_support.TeeIfVerbose,
		runner_support.TeeIfVerbose,
	)
	Ω(err).ShouldNot(HaveOccurred())
	Ω(stagerSession).Should(SayWithTimeout("Listening for staging requests!", 1*time.Second))
	r.stagerSession = stagerSession
}

func (r *StagerRunner) Stop() {
	r.stagerSession.Cmd.Process.Signal(syscall.SIGTERM)
}
