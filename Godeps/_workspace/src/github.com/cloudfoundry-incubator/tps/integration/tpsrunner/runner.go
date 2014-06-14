package tpsrunner

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/onsi/ginkgo"
	"github.com/onsi/gomega"
	"github.com/onsi/gomega/gbytes"
	"github.com/onsi/gomega/gexec"
	"github.com/tedsuo/ifrit"
)

type runner struct {
	bin               string
	listenAddr        string
	etcdCluster       []string
	natsAddresses     []string
	heartbeatInterval time.Duration
}

func New(bin string, listenAddr string, etcdCluster []string, natsAddresses []string, heartbeatInterval time.Duration) ifrit.Runner {
	return &runner{
		bin:               bin,
		listenAddr:        listenAddr,
		etcdCluster:       etcdCluster,
		natsAddresses:     natsAddresses,
		heartbeatInterval: heartbeatInterval,
	}
}

func (r *runner) Run(signals <-chan os.Signal, ready chan<- struct{}) error {
	session, err := gexec.Start(
		exec.Command(
			r.bin,
			"-etcdCluster", strings.Join(r.etcdCluster, ","),
			"-natsAddresses", strings.Join(r.natsAddresses, ","),
			"-heartbeatInterval", fmt.Sprintf("%s", r.heartbeatInterval),
			"-listenAddr", r.listenAddr,
		),
		gexec.NewPrefixedWriter("\x1b[32m[o]\x1b[31m[tps]\x1b[0m ", ginkgo.GinkgoWriter),
		gexec.NewPrefixedWriter("\x1b[91m[e]\x1b[31m[tps]\x1b[0m ", ginkgo.GinkgoWriter),
	)
	if err != nil {
		return err
	}

	gomega.Eventually(session).Should(gbytes.Say("tps.started"))

	close(ready)

dance:
	for {
		select {
		case sig := <-signals:
			session.Signal(sig)
		case <-session.Exited:
			break dance
		}
	}

	if session.ExitCode() == 0 {
		return nil
	}

	return fmt.Errorf("exit status %d", session.ExitCode())
}
