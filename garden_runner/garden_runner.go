package garden_runner

import (
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/onsi/ginkgo/config"
	"github.com/vito/cmdtest"
	"github.com/vito/gordon"
)

type GardenRunner struct {
	Port int

	DepotPath     string
	RootPath      string
	RootFSPath    string
	SnapshotsPath string

	gardenBin string
	gardenCmd *exec.Cmd

	tmpdir string
}

func New(rootPath string, rootFSPath string) (*GardenRunner, error) {
	runner := &GardenRunner{
		Port:       config.GinkgoConfig.ParallelNode + 7012,
		RootPath:   rootPath,
		RootFSPath: rootFSPath,
	}

	return runner, runner.Prepare()
}

func (r *GardenRunner) cmd(command string, argv ...string) *exec.Cmd {
	return exec.Command(command, argv...)
}

func (r *GardenRunner) Prepare() error {
	r.tmpdir = fmt.Sprintf("/tmp/garden-%d-%d", time.Now().UnixNano(), config.GinkgoConfig.ParallelNode)
	err := r.cmd("mkdir", r.tmpdir).Run()
	if err != nil {
		return err
	}

	compiled, err := cmdtest.Build("github.com/pivotal-cf-experimental/garden")
	if err != nil {
		return err
	}

	r.gardenBin = compiled

	r.DepotPath = filepath.Join(r.tmpdir, "containers")
	err = r.cmd("mkdir", "-m", "0755", r.DepotPath).Run()
	if err != nil {
		return err
	}

	r.SnapshotsPath = filepath.Join(r.tmpdir, "snapshots")
	return r.cmd("mkdir", r.SnapshotsPath).Run()
}

func (r *GardenRunner) Start(argv ...string) error {
	gardenArgs := argv
	gardenArgs = append(
		gardenArgs,
		"--listenNetwork", "tcp",
		"--listenAddr", fmt.Sprintf(":%d", r.Port),
		"--root", r.RootPath,
		"--depot", r.DepotPath,
		"--rootfs", r.RootFSPath,
		"--snapshots", r.SnapshotsPath,
		"--debug",
	)

	garden := r.cmd("sudo", append([]string{r.gardenBin}, gardenArgs...)...)

	garden.Stdout = os.Stdout
	garden.Stderr = os.Stderr

	err := garden.Start()
	if err != nil {
		return err
	}

	started := make(chan bool, 1)
	stop := make(chan bool, 1)

	go r.waitForStart(started, stop)

	timeout := 1 * time.Minute

	r.gardenCmd = garden

	select {
	case <-started:
		return nil
	case <-time.After(timeout):
		stop <- true
		return fmt.Errorf("garden did not come up within %s", timeout)
	}
}

func (r *GardenRunner) Stop() error {
	if r.gardenCmd == nil {
		return nil
	}

	stopCmd := exec.Command("sudo", "kill", fmt.Sprintf("%d", r.gardenCmd.Process.Pid))
	stopCmd.Stderr = os.Stderr
	stopCmd.Stdout = os.Stdout

	err := stopCmd.Run()
	if err != nil {
		return err
	}

	stopped := make(chan bool, 1)
	stop := make(chan bool, 1)

	go r.waitForStop(stopped, stop)

	timeout := 10 * time.Second

	select {
	case <-stopped:
		r.gardenCmd = nil
		return nil
	case <-time.After(timeout):
		stop <- true
		return fmt.Errorf("garden did not shut down within %s", timeout)
	}
}

func (r *GardenRunner) DestroyContainers() error {
	lsOutput, err := r.cmd("find", r.DepotPath, "-maxdepth", "1", "-mindepth", "1", "-print0").Output() // ls does not use linebreaks
	if err != nil {
		return err
	}

	containerDirs := strings.Split(string(lsOutput), "\x00")

	for _, dir := range containerDirs {
		if dir == "" {
			continue
		}

		err := r.cmd(
			"sudo",
			filepath.Join(r.RootPath, "linux", "destroy.sh"),
			dir,
		).Run()

		if err != nil {
			return err
		}
	}

	if r.SnapshotsPath != "" {
		err := r.cmd("rm", "-rf", r.SnapshotsPath).Run()
		if err != nil {
			return err
		}
	}

	return nil
}

func (r *GardenRunner) TearDown() error {
	err := r.DestroyContainers()
	if err != nil {
		return err
	}

	return r.cmd("rm", "-rf", r.tmpdir).Run()
}

func (r *GardenRunner) NewClient() gordon.Client {
	return gordon.NewClient(&gordon.ConnectionInfo{
		Network: "tcp",
		Addr:    r.addr(),
	})
}

func (r *GardenRunner) waitForStart(started chan<- bool, stop <-chan bool) {
	for {
		var err error

		conn, dialErr := net.Dial("tcp", r.addr())

		if dialErr == nil {
			conn.Close()
		}

		err = dialErr

		if err == nil {
			started <- true
			return
		}

		select {
		case <-stop:
			return
		case <-time.After(100 * time.Millisecond):
		}
	}
}

func (r *GardenRunner) waitForStop(stopped chan<- bool, stop <-chan bool) {
	for {
		var err error

		conn, dialErr := net.Dial("tcp", r.addr())

		if dialErr == nil {
			conn.Close()
		}

		err = dialErr

		if err != nil {
			stopped <- true
			return
		}

		select {
		case <-stop:
			return
		case <-time.After(100 * time.Millisecond):
		}
	}
}

func (r *GardenRunner) addr() string {
	return fmt.Sprintf("127.0.0.1:%d", r.Port)
}
