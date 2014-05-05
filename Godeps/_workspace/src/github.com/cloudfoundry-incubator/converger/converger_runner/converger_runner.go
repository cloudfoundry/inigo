package converger_runner

import (
	"os/exec"
	"syscall"
	"time"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/onsi/gomega/gbytes"
	"github.com/onsi/gomega/gexec"
)

type ConvergerRunner struct {
	binPath string
	Session *gexec.Session
	config  Config
}

type Config struct {
	etcdCluster string
	logLevel    string
}

func New(binPath, etcdCluster, logLevel string) *ConvergerRunner {
	return &ConvergerRunner{
		binPath: binPath,
		config: Config{
			etcdCluster: etcdCluster,
			logLevel:    logLevel,
		},
	}
}

func (r *ConvergerRunner) Start(convergenceInterval, timeToClaim time.Duration) {
	convergerSession, err := gexec.Start(
		exec.Command(
			r.binPath,
			"-etcdCluster", r.config.etcdCluster,
			"-logLevel", r.config.logLevel,
			"-convergenceInterval", convergenceInterval.String(),
			"-timeToClaimTask", timeToClaim.String(),
		),
		GinkgoWriter,
		GinkgoWriter,
	)

	Ω(err).ShouldNot(HaveOccurred())
	r.Session = convergerSession
	Eventually(r.Session.Buffer()).Should(gbytes.Say("started"))
}

func (r *ConvergerRunner) Stop() {
	if r.Session != nil {
		r.Session.Command.Process.Signal(syscall.SIGTERM)
		r.Session.Wait(5 * time.Second)
	}
}

func (r *ConvergerRunner) KillWithFire() {
	if r.Session != nil {
		r.Session.Command.Process.Kill()
	}
}
