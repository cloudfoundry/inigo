package app_manager_runner

import (
	"encoding/json"
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
	natsCluster   []string
	healthChecks  map[string]string
	Session       *gexec.Session

	repAddrRelativeToExecutor string
}

func New(
	appManagerBin string,
	etcdCluster,
	natsCluster []string,
	healthChecks map[string]string,
	repAddrRelativeToExecutor string,
) *AppManagerRunner {
	return &AppManagerRunner{
		appManagerBin: appManagerBin,
		etcdCluster:   etcdCluster,
		natsCluster:   natsCluster,
		healthChecks:  healthChecks,

		repAddrRelativeToExecutor: repAddrRelativeToExecutor,
	}
}

func (r *AppManagerRunner) Start() {
	r.StartWithoutCheck()
	Eventually(r.Session, 5*time.Second).Should(gbytes.Say("app_manager.started"))
}

func (r *AppManagerRunner) StartWithoutCheck() {
	healthChecksFlag, err := json.Marshal(r.healthChecks)
	Ω(err).ShouldNot(HaveOccurred())

	executorSession, err := gexec.Start(
		exec.Command(
			r.appManagerBin,
			"-etcdCluster", strings.Join(r.etcdCluster, ","),
			"-natsAddresses", strings.Join(r.natsCluster, ","),
			"-healthChecks", string(healthChecksFlag),
			"-repAddrRelativeToExecutor", r.repAddrRelativeToExecutor,
		),
		ginkgo.GinkgoWriter,
		ginkgo.GinkgoWriter,
	)
	Ω(err).ShouldNot(HaveOccurred())

	r.Session = executorSession
}

func (r *AppManagerRunner) Stop() {
	if r.Session != nil {
		r.Session.Terminate().Wait(5 * time.Second)
	}
}

func (r *AppManagerRunner) KillWithFire() {
	if r.Session != nil {
		r.Session.Kill().Wait(5 * time.Second)
	}
}
