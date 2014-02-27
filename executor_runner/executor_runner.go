package executor_runner

import (
	"fmt"
	"github.com/nu7hatch/gouuid"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"

	"github.com/cloudfoundry/gunk/runner_support"
	. "github.com/onsi/gomega"
	"github.com/vito/cmdtest"
	. "github.com/vito/cmdtest/matchers"
)

type ExecutorRunner struct {
	executorBin   string
	wardenNetwork string
	wardenAddr    string
	etcdMachines  []string
	snapshotFile  string

	loggregatorServer string
	loggregatorSecret string

	Session *cmdtest.Session
}

type Config struct {
	MemoryMB            int
	DiskMB              int
	SnapshotFile        string
	ConvergenceInterval int
	HeartbeatInterval   int
	Stack               string
	TempDir             string
}

var defaultConfig = Config{
	MemoryMB:            1024,
	DiskMB:              1024,
	ConvergenceInterval: 30,
	HeartbeatInterval:   60,
	Stack:               "default",
	TempDir:             "/tmp",
}

func New(executorBin, wardenNetwork, wardenAddr string, etcdMachines []string, loggregatorServer string, loggregatorSecret string) *ExecutorRunner {
	return &ExecutorRunner{
		executorBin:   executorBin,
		wardenNetwork: wardenNetwork,
		wardenAddr:    wardenAddr,
		etcdMachines:  etcdMachines,

		loggregatorServer: loggregatorServer,
		loggregatorSecret: loggregatorSecret,
	}
}

func (r *ExecutorRunner) Start(config ...Config) {
	r.StartWithoutCheck(config...)

	Ω(r.Session).Should(SayWithTimeout("Watching for RunOnces!", 1*time.Second))
}

func (r *ExecutorRunner) StartWithoutCheck(config ...Config) {
	configToUse := r.generateConfig(config...)
	executorSession, err := cmdtest.StartWrapped(
		exec.Command(
			r.executorBin,
			"-wardenNetwork", r.wardenNetwork,
			"-wardenAddr", r.wardenAddr,
			"-etcdMachines", strings.Join(r.etcdMachines, ","),
			"-memoryMB", fmt.Sprintf("%d", configToUse.MemoryMB),
			"-diskMB", fmt.Sprintf("%d", configToUse.DiskMB),
			"-registrySnapshotFile", configToUse.SnapshotFile,
			"-convergenceInterval", fmt.Sprintf("%d", configToUse.ConvergenceInterval),
			"-heartbeatInterval", fmt.Sprintf("%d", configToUse.HeartbeatInterval),
			"-stack", configToUse.Stack,
			"-loggregatorServer", r.loggregatorServer,
			"-loggregatorSecret", r.loggregatorSecret,
			"-tempDir", configToUse.TempDir,
		),
		runner_support.TeeToGinkgoWriter,
		runner_support.TeeToGinkgoWriter,
	)
	Ω(err).ShouldNot(HaveOccurred())
	r.snapshotFile = configToUse.SnapshotFile
	r.Session = executorSession
}

func (r *ExecutorRunner) Stop() {
	if r.Session != nil {
		r.Session.Cmd.Process.Signal(syscall.SIGTERM)
	}
}

func (r *ExecutorRunner) KillWithFire() {
	if r.Session != nil {
		r.Session.Cmd.Process.Signal(syscall.SIGKILL)
		os.Remove(r.snapshotFile)
	}
}

func (r *ExecutorRunner) generateConfig(config ...Config) Config {
	guid, _ := uuid.NewV4()
	snapshotFile := fmt.Sprintf("/tmp/executor_registry_%s", guid.String())
	configToReturn := defaultConfig
	configToReturn.SnapshotFile = snapshotFile

	if len(config) == 0 {
		return configToReturn
	}

	givenConfig := config[0]
	if givenConfig.MemoryMB != 0 {
		configToReturn.MemoryMB = givenConfig.MemoryMB
	}
	if givenConfig.DiskMB != 0 {
		configToReturn.DiskMB = givenConfig.DiskMB
	}
	if givenConfig.SnapshotFile != "" {
		configToReturn.SnapshotFile = givenConfig.SnapshotFile
	}
	if givenConfig.ConvergenceInterval != 0 {
		configToReturn.ConvergenceInterval = givenConfig.ConvergenceInterval
	}
	if givenConfig.HeartbeatInterval != 0 {
		configToReturn.HeartbeatInterval = givenConfig.HeartbeatInterval
	}
	if givenConfig.Stack != "" {
		configToReturn.Stack = givenConfig.Stack
	}
	if givenConfig.TempDir != "" {
		configToReturn.TempDir = givenConfig.TempDir
	}

	return configToReturn
}
