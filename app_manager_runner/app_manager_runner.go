package app_manager_runner

import (
	"os/exec"
	"strings"
	"time"

	"github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/onsi/gomega/gbytes"
	"github.com/onsi/gomega/gexec"
)

type AppManagerRunner struct {
	appManagerBin string
	etcdCluster   []string
	natsAddresses []string
	session       *gexec.Session
}

func New(appManagerBin string, etcdCluster []string, natsAddresses []string) *AppManagerRunner {
	return &AppManagerRunner{
		appManagerBin: appManagerBin,
		etcdCluster:   etcdCluster,
		natsAddresses: natsAddresses,
	}
}

func (r *AppManagerRunner) Start(args ...string) {
	appManagerSession, err := gexec.Start(
		exec.Command(
			r.appManagerBin,
			append([]string{
				"-etcdCluster", strings.Join(r.etcdCluster, ","),
				"-natsAddresses", strings.Join(r.natsAddresses, ","),
			}, args...)...,
		),
		ginkgo.GinkgoWriter,
		ginkgo.GinkgoWriter,
	)

	Ω(err).ShouldNot(HaveOccurred())
	Eventually(appManagerSession).Should(gbytes.Say("app_manager.started"))

	r.session = appManagerSession
}

func (r *AppManagerRunner) Stop() {
	if r.session != nil {
		r.session.Interrupt().Wait(5 * time.Second)
	}
}
