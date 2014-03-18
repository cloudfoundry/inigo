package stager_runner

import (
	"os/exec"
	"strings"
	"syscall"
	"time"

	"github.com/cloudfoundry/gunk/runner_support"
	. "github.com/onsi/gomega"
	"github.com/vito/cmdtest"
	. "github.com/vito/cmdtest/matchers"
)

type StagerRunner struct {
	stagerBin     string
	etcdCluster   []string
	natsAddresses []string

	session     *cmdtest.Session
	CompilerUrl string
}

func New(stagerBin string, etcdCluster []string, natsAddresses []string) *StagerRunner {
	return &StagerRunner{
		stagerBin:     stagerBin,
		etcdCluster:   etcdCluster,
		natsAddresses: natsAddresses,
	}
}

func (r *StagerRunner) Start(args ...string) {
	stagerSession, err := cmdtest.StartWrapped(
		exec.Command(
			r.stagerBin,
			append([]string{
				"-etcdCluster", strings.Join(r.etcdCluster, ","),
				"-natsAddresses", strings.Join(r.natsAddresses, ","),
			}, args...)...,
		),
		runner_support.TeeToGinkgoWriter,
		runner_support.TeeToGinkgoWriter,
	)

	Ω(err).ShouldNot(HaveOccurred())
	Ω(stagerSession).Should(SayWithTimeout(
		"Listening for staging requests!",
		1*time.Second,
	))

	r.session = stagerSession
}

func (r *StagerRunner) Stop() {
	if r.session != nil {
		r.session.Cmd.Process.Signal(syscall.SIGTERM)
		processState := r.session.Cmd.ProcessState
		if processState != nil && processState.Exited() {
			return
		}

		r.session.Wait(5 * time.Second)
		Ω(r.session.Cmd.ProcessState.Exited()).Should(BeTrue())
	}
}
