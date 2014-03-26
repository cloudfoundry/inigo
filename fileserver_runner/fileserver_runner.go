package fileserver_runner

import (
	"fmt"
	"io/ioutil"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/cloudfoundry/gunk/runner_support"
	. "github.com/onsi/gomega"
	"github.com/vito/cmdtest"
)

type FileServerRunner struct {
	fileServerBin string
	etcdCluster   []string
	dir           string
	port          int
	Session       *cmdtest.Session
	ccAddress     string
	ccUsername    string
	ccPassword    string
}

func New(fileServerBin string, port int, etcdCluster []string, ccAddress, ccUsername, ccPassword string) *FileServerRunner {
	return &FileServerRunner{
		fileServerBin: fileServerBin,
		etcdCluster:   etcdCluster,
		port:          port,
		ccAddress:     ccAddress,
		ccUsername:    ccUsername,
		ccPassword:    ccPassword,
	}
}

func (r *FileServerRunner) Start() {
	tempDir, err := ioutil.TempDir("", "inigo-file-server")
	Ω(err).ShouldNot(HaveOccurred())
	r.dir = tempDir
	ioutil.WriteFile(filepath.Join(r.dir, "ready"), []byte("ready"), os.ModePerm)

	executorSession, err := cmdtest.StartWrapped(
		exec.Command(
			r.fileServerBin,
			"-address", "127.0.0.1",
			"-port", fmt.Sprintf("%d", r.port),
			"-etcdCluster", strings.Join(r.etcdCluster, ","),
			"-staticDirectory", r.dir,
			"-ccJobPollingInterval", "100ms",
			"-ccAddress", r.ccAddress,
			"-ccUsername", r.ccUsername,
			"-ccPassword", r.ccPassword,
		),
		runner_support.TeeToGinkgoWriter,
		runner_support.TeeToGinkgoWriter,
	)
	Ω(err).ShouldNot(HaveOccurred())
	r.Session = executorSession

	Eventually(func() int {
		resp, _ := http.Get(fmt.Sprintf("http://127.0.0.1:%d/static/ready", r.port))
		if resp != nil {
			return resp.StatusCode
		} else {
			return 0
		}
	}).Should(Equal(http.StatusOK))
}

func (r *FileServerRunner) ServeFile(name string, path string) {
	data, err := ioutil.ReadFile(path)
	Ω(err).ShouldNot(HaveOccurred())
	ioutil.WriteFile(filepath.Join(r.dir, name), data, os.ModePerm)
}

func (r *FileServerRunner) Stop() {
	if r.Session != nil {
		r.Session.Cmd.Process.Signal(syscall.SIGTERM)
		_, err := r.Session.Wait(5 * time.Second)
		Ω(err).ShouldNot(HaveOccurred())
	}
}
