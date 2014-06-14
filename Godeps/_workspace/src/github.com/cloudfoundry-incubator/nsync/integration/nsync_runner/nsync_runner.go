package nsync_runner

import (
	"os/exec"
	"strings"
	"time"

	"github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/onsi/gomega/gbytes"
	"github.com/onsi/gomega/gexec"
)

type NsyncRunner struct {
	nsyncBin    string
	etcdCluster []string
	natsCluster []string
	Session     *gexec.Session

	repAddrRelativeToExecutor string
}

func New(
	nsyncBin string,
	etcdCluster, natsCluster []string,
) *NsyncRunner {
	return &NsyncRunner{
		nsyncBin:    nsyncBin,
		etcdCluster: etcdCluster,
		natsCluster: natsCluster,
	}
}

func (r *NsyncRunner) Start() {
	r.StartWithoutCheck()
	Eventually(r.Session, 5*time.Second).Should(gbytes.Say("nsync.started"))
}

func (r *NsyncRunner) StartWithoutCheck() {
	executorSession, err := gexec.Start(
		exec.Command(
			r.nsyncBin,
			"-etcdCluster", strings.Join(r.etcdCluster, ","),
			"-natsAddresses", strings.Join(r.natsCluster, ","),
		),
		gexec.NewPrefixedWriter("\x1b[32m[o]\x1b[35m[nsync]\x1b[0m ", ginkgo.GinkgoWriter),
		gexec.NewPrefixedWriter("\x1b[91m[e]\x1b[35m[nsync]\x1b[0m ", ginkgo.GinkgoWriter),
	)
	Ω(err).ShouldNot(HaveOccurred())

	r.Session = executorSession
}

func (r *NsyncRunner) Stop() {
	if r.Session != nil {
		r.Session.Terminate().Wait(5 * time.Second)
	}
}

func (r *NsyncRunner) KillWithFire() {
	if r.Session != nil {
		r.Session.Kill().Wait(5 * time.Second)
	}
}
