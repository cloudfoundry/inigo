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
	Session       *gexec.Session
}

func New(appManagerBin string, etcdCluster []string) *AppManagerRunner {
	return &AppManagerRunner{
		appManagerBin: appManagerBin,
		etcdCluster:   etcdCluster,
	}
}

func (r *AppManagerRunner) Start() {
	r.StartWithoutCheck()
	Eventually(r.Session, 5*time.Second).Should(gbytes.Say("app-manager.started"))
}

func (r *AppManagerRunner) StartWithoutCheck() {
	executorSession, err := gexec.Start(
		exec.Command(
			r.appManagerBin,
			"-etcdCluster", strings.Join(r.etcdCluster, ","),
		),
		gexec.NewPrefixedWriter("\x1b[32m[o]\x1b[35m[app-manager]\x1b[0m ", ginkgo.GinkgoWriter),
		gexec.NewPrefixedWriter("\x1b[91m[e]\x1b[35m[app-manager]\x1b[0m ", ginkgo.GinkgoWriter),
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
