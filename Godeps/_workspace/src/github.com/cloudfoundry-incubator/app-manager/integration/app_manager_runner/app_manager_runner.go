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
	circuses      map[string]string
	Session       *gexec.Session

	repAddrRelativeToExecutor string
}

func New(
	appManagerBin string,
	etcdCluster,
	natsCluster []string,
	circuses map[string]string,
	repAddrRelativeToExecutor string,
) *AppManagerRunner {
	return &AppManagerRunner{
		appManagerBin: appManagerBin,
		etcdCluster:   etcdCluster,
		natsCluster:   natsCluster,
		circuses:      circuses,

		repAddrRelativeToExecutor: repAddrRelativeToExecutor,
	}
}

func (r *AppManagerRunner) Start() {
	r.StartWithoutCheck()
	Eventually(r.Session, 5*time.Second).Should(gbytes.Say("app_manager.started"))
}

func (r *AppManagerRunner) StartWithoutCheck() {
	circusesFlag, err := json.Marshal(r.circuses)
	Ω(err).ShouldNot(HaveOccurred())

	executorSession, err := gexec.Start(
		exec.Command(
			r.appManagerBin,
			"-etcdCluster", strings.Join(r.etcdCluster, ","),
			"-natsAddresses", strings.Join(r.natsCluster, ","),
			"-circuses", string(circusesFlag),
			"-repAddrRelativeToExecutor", r.repAddrRelativeToExecutor,
		),
		gexec.NewPrefixedWriter("\x1b[32m[o]\x1b[35m[app_manager]\x1b[0m ", ginkgo.GinkgoWriter),
		gexec.NewPrefixedWriter("\x1b[91m[e]\x1b[35m[app_manager]\x1b[0m ", ginkgo.GinkgoWriter),
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
