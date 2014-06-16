package runner

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/onsi/ginkgo"
	"github.com/onsi/gomega"
	"github.com/onsi/gomega/gbytes"
	"github.com/onsi/gomega/gexec"
	"github.com/tedsuo/ifrit"
)

func NewRunner(startedMessage string, bin string, argv ...string) ifrit.Runner {
	return ifrit.RunFunc(func(signals <-chan os.Signal, ready chan<- struct{}) error {
		name := filepath.Base(bin)

		session, err := gexec.Start(
			exec.Command(bin, argv...),
			gexec.NewPrefixedWriter("\x1b[32m[o]\x1b[35m["+name+"]\x1b[0m ", ginkgo.GinkgoWriter),
			gexec.NewPrefixedWriter("\x1b[91m[e]\x1b[35m["+name+"]\x1b[0m ", ginkgo.GinkgoWriter),
		)

		if err != nil {
			return err
		}

		gomega.Eventually(session).Should(gbytes.Say(startedMessage))

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
	})
}
