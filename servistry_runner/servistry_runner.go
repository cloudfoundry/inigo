package servistry_runner

import (
	"github.com/cloudfoundry/gunk/runner_support"
	. "github.com/onsi/gomega"
	"github.com/vito/cmdtest"
	. "github.com/vito/cmdtest/matchers"
	"os/exec"
	"strings"
	"syscall"
	"time"
)

type ServistryRunner struct {
	binPath       string
	etcdCluster   []string
	natsAddresses []string

	session *cmdtest.Session
}

func New(binPath string, etcdCluster []string, natsAddresses []string) *ServistryRunner {
	return &ServistryRunner{
		binPath:       binPath,
		etcdCluster:   etcdCluster,
		natsAddresses: natsAddresses,
	}
}

func (r *ServistryRunner) Start(args ...string) {
	session, err := cmdtest.StartWrapped(
		exec.Command(
			r.binPath,
			append([]string{
				"-etcdCluster", strings.Join(r.etcdCluster, ","),
				"-natsAddresses", strings.Join(r.natsAddresses, ","),
			}, args...)...,
		),
		runner_support.TeeToGinkgoWriter,
		runner_support.TeeToGinkgoWriter,
	)

	Ω(err).ShouldNot(HaveOccurred())
	Ω(session).Should(SayWithTimeout(
		"servistry started",
		1*time.Second,
	))

	r.session = session
}

func (r *ServistryRunner) Stop() {
	if r.session != nil {
		r.session.Cmd.Process.Signal(syscall.SIGTERM)
		_, err := r.session.Wait(5 * time.Second)
		Ω(err).ShouldNot(HaveOccurred())
	}
}
