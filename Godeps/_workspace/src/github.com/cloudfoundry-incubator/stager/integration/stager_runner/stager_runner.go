package stager_runner

import (
	"os/exec"
	"strings"
	"time"

	"github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/onsi/gomega/gbytes"
	"github.com/onsi/gomega/gexec"
)

type StagerRunner struct {
	stagerBin     string
	etcdCluster   []string
	natsAddresses []string

	session     *gexec.Session
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
	if r.session != nil {
		panic("starting more than one stager runner!!!")
	}

	stagerSession, err := gexec.Start(
		exec.Command(
			r.stagerBin,
			append([]string{
				"-etcdCluster", strings.Join(r.etcdCluster, ","),
				"-natsAddresses", strings.Join(r.natsAddresses, ","),
			}, args...)...,
		),
		gexec.NewPrefixedWriter("\x1b[32m[o]\x1b[95m[stager]\x1b[0m ", ginkgo.GinkgoWriter),
		gexec.NewPrefixedWriter("\x1b[91m[e]\x1b[95m[stager]\x1b[0m ", ginkgo.GinkgoWriter),
	)

	Ω(err).ShouldNot(HaveOccurred())
	Eventually(stagerSession).Should(gbytes.Say("Listening for staging requests!"))

	r.session = stagerSession
}

func (r *StagerRunner) Stop() {
	if r.session != nil {
		r.session.Interrupt().Wait(5 * time.Second)
		r.session = nil
	}
}

func (r *StagerRunner) KillWithFire() {
	if r.session != nil {
		r.session.Kill().Wait(5 * time.Second)
		r.session = nil
	}
}

func (r *StagerRunner) Session() *gexec.Session {
	return r.session
}
