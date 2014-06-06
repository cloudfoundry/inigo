package route_emitter_runner

import (
	"os/exec"
	"strings"
	"time"

	"github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/onsi/gomega/gbytes"
	"github.com/onsi/gomega/gexec"
)

type Runner struct {
	emitterBin  string
	etcdCluster []string
	natsCluster []string
	Session     *gexec.Session
}

func New(emitterBin string, etcdCluster, natsCluster []string) *Runner {
	return &Runner{
		emitterBin:  emitterBin,
		etcdCluster: etcdCluster,
		natsCluster: natsCluster,
	}
}

func (r *Runner) Start() {
	r.StartWithoutCheck()
	Eventually(r.Session, 5*time.Second).Should(gbytes.Say("route-emitter.started"))
}

func (r *Runner) StartWithoutCheck() {
	executorSession, err := gexec.Start(
		exec.Command(
			r.emitterBin,
			"-etcdCluster", strings.Join(r.etcdCluster, ","),
			"-natsAddresses", strings.Join(r.natsCluster, ","),
		),
		gexec.NewPrefixedWriter("\x1b[32m[o]\x1b[97m[route-emitter]\x1b[0m ", ginkgo.GinkgoWriter),
		gexec.NewPrefixedWriter("\x1b[91m[e]\x1b[97m[route-emitter]\x1b[0m ", ginkgo.GinkgoWriter),
	)
	Ω(err).ShouldNot(HaveOccurred())
	r.Session = executorSession
}

func (r *Runner) Stop() {
	if r.Session != nil {
		r.Session.Terminate().Wait(5 * time.Second)
	}
}

func (r *Runner) KillWithFire() {
	if r.Session != nil {
		r.Session.Kill().Wait(5 * time.Second)
	}
}
