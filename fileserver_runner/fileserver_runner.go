package fileserver_runner

import (
	"fmt"
	"io/ioutil"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/onsi/gomega/gexec"
)

type FileServerRunner struct {
	fileServerBin string
	etcdCluster   []string
	dir           string
	port          int
	Session       *gexec.Session
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

	executorSession, err := gexec.Start(
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
		gexec.NewPrefixedWriter("\x1b[32m[o]\x1b[91m[fileserver]\x1b[0m ", ginkgo.GinkgoWriter),
		gexec.NewPrefixedWriter("\x1b[91m[e]\x1b[91m[fileserver]\x1b[0m ", ginkgo.GinkgoWriter),
	)
	Ω(err).ShouldNot(HaveOccurred())

	r.Session = executorSession

	Eventually(func() int {
		resp, _ := http.Get(fmt.Sprintf("http://127.0.0.1:%d/v1/static/ready", r.port))
		if resp != nil {
			return resp.StatusCode
		} else {
			return 0
		}
	}, 5).Should(Equal(http.StatusOK))
}

func (r *FileServerRunner) ServeFile(name string, path string) {
	data, err := ioutil.ReadFile(path)
	Ω(err).ShouldNot(HaveOccurred())

	ioutil.WriteFile(filepath.Join(r.dir, name), data, os.ModePerm)
}

func (r *FileServerRunner) Stop() {
	if r.Session != nil {
		r.Session.Interrupt().Wait(5 * time.Second)
		r.Session = nil
	}
}

func (r *FileServerRunner) KillWithFire() {
	if r.Session != nil {
		r.Session.Kill().Wait(5 * time.Second)
		r.Session = nil
	}
}
