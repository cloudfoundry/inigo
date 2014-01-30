package executor_runner

import (
	"io"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"

	. "github.com/onsi/gomega"
	"github.com/vito/cmdtest"
	. "github.com/vito/cmdtest/matchers"
)

type ExecutorRunner struct {
	executorBin   string
	wardenNetwork string
	wardenAddr    string
	etcdMachines  []string

	executorSession *cmdtest.Session
}

func New(executorBin, wardenNetwork, wardenAddr string, etcdMachines []string) *ExecutorRunner {
	return &ExecutorRunner{
		executorBin:   executorBin,
		wardenNetwork: wardenNetwork,
		wardenAddr:    wardenAddr,
		etcdMachines:  etcdMachines,
	}
}

func (r *ExecutorRunner) Start() {
	executorSession, err := cmdtest.StartWrapped(
		exec.Command(
			r.executorBin,
			"-wardenNetwork", r.wardenNetwork,
			"-wardenAddr", r.wardenAddr,
			"-etcdMachines", strings.Join(r.etcdMachines, ","),
		),
		teeToStdout,
		teeToStdout,
	)
	Ω(err).ShouldNot(HaveOccurred())

	Ω(executorSession).Should(SayWithTimeout("Watching for RunOnces!", 1*time.Second))

	r.executorSession = executorSession
}

func (r *ExecutorRunner) Stop() {
	if r.executorSession != nil {
		r.executorSession.Cmd.Process.Signal(syscall.SIGTERM)
	}
}

func teeToStdout(out io.Writer) io.Writer {
	return io.MultiWriter(out, os.Stdout)
}
