package stager_runner

import (
	. "github.com/onsi/gomega"
	"github.com/vito/cmdtest"
	. "github.com/vito/cmdtest/matchers"
	"io"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"
)

type StagerRunner struct {
	stagerBin    string
	etcdMachines []string

	stagerSession *cmdtest.Session
}

func New(stagerBin string, etcdMachines []string) *StagerRunner {
	return &StagerRunner{
		stagerBin:    stagerBin,
		etcdMachines: etcdMachines,
	}
}

func (r *StagerRunner) Start() {
	stagerSession, err := cmdtest.StartWrapped(
		exec.Command(
			r.stagerBin,
			"-etcdMachines", strings.Join(r.etcdMachines, ","),
		),
		teeToStdout,
		teeToStdout,
	)
	Ω(err).ShouldNot(HaveOccurred())
	Ω(stagerSession).Should(SayWithTimeout("Listening for staging requests!", 1*time.Second))
	r.stagerSession = stagerSession
}

func (r *StagerRunner) Stop() {
	r.stagerSession.Cmd.Process.Signal(syscall.SIGTERM)
}

//copy-pasta from executor runner.  maybe this should be somewhere else?
func teeToStdout(out io.Writer) io.Writer {
	return io.MultiWriter(out, os.Stdout)
}
