package fileserver_runner

import (
	"fmt"
	"io/ioutil"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/cloudfoundry/gunk/runner_support"
	. "github.com/onsi/gomega"
	"github.com/vito/cmdtest"
	. "github.com/vito/cmdtest/matchers"
)

type FileServerRunner struct {
	fileServerBin string
	etcdMachines  []string
	dir           string
	port          int
	Session       *cmdtest.Session
}

func New(fileServerBin string, port int, etcdMachines []string) *FileServerRunner {
	tempDir, err := ioutil.TempDir("", "inigo-file-server")
	Ω(err).ShouldNot(HaveOccurred())
	return &FileServerRunner{
		fileServerBin: fileServerBin,
		etcdMachines:  etcdMachines,
		port:          port,
		dir:           tempDir,
	}
}

func (r *FileServerRunner) Start() {
	executorSession, err := cmdtest.StartWrapped(
		exec.Command(
			r.fileServerBin,
			"-address", "127.0.0.1",
			"-port", fmt.Sprintf("%d", r.port),
			"-etcdMachines", strings.Join(r.etcdMachines, ","),
			"-directory", r.dir,
		),
		runner_support.TeeIfVerbose,
		runner_support.TeeIfVerbose,
	)
	Ω(err).ShouldNot(HaveOccurred())
	r.Session = executorSession

	Ω(r.Session).Should(SayWithTimeout("Serving files on", 1*time.Second))
	time.Sleep(10 * time.Millisecond)
}

func (r *FileServerRunner) ServeFile(name string, path string) {
	data, err := ioutil.ReadFile(path)
	Ω(err).ShouldNot(HaveOccurred())
	ioutil.WriteFile(filepath.Join(r.dir, name), data, os.ModePerm)
}

func (r *FileServerRunner) Stop() {
	os.RemoveAll(r.dir)
	if r.Session != nil {
		r.Session.Cmd.Process.Signal(syscall.SIGTERM)
	}
}
