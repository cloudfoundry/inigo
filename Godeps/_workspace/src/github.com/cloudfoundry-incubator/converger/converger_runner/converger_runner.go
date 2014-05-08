package converger_runner

import (
	"os/exec"
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
	if r.Session != nil {
		panic("starting two convergers!!!")
	}

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
	Eventually(r.Session, 5*time.Second).Should(gbytes.Say("started"))
}

func (r *ConvergerRunner) Stop() {
	if r.Session != nil {
		r.Session.Interrupt().Wait(5 * time.Second)
		r.Session = nil
	}
}

func (r *ConvergerRunner) KillWithFire() {
	if r.Session != nil {
		r.Session.Kill().Wait()
		r.Session = nil
	}
}
