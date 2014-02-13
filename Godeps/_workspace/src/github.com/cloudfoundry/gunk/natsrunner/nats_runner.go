package natsrunner

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"

	"github.com/cloudfoundry/yagnats"
	. "github.com/onsi/gomega"
	"github.com/vito/cmdtest"
)

var natsCommand *exec.Cmd

type NATSRunner struct {
	port        int
	natsSession *cmdtest.Session
	MessageBus  yagnats.NATSClient
}

func NewNATSRunner(port int) *NATSRunner {
	return &NATSRunner{
		port: port,
	}
}

func (runner *NATSRunner) Start() {
	_, err := exec.LookPath("gnatsd")
	if err != nil {
		fmt.Println("You need gnatsd installed!")
		os.Exit(1)
	}

	sess, err := cmdtest.Start(exec.Command("gnatsd", "-p", strconv.Itoa(runner.port)))
	Ω(err).ShouldNot(HaveOccurred(), "Make sure to have gnatsd on your path")

	runner.natsSession = sess

	connectionInfo := &yagnats.ConnectionInfo{
		Addr: fmt.Sprintf("127.0.0.1:%d", runner.port),
	}

	messageBus := yagnats.NewClient()

	Eventually(func() error {
		return messageBus.Connect(connectionInfo)
	}, 5, 0.1).ShouldNot(HaveOccurred())

	runner.MessageBus = messageBus
}

func (runner *NATSRunner) Stop() {
	if runner.natsSession != nil {
		runner.natsSession.Cmd.Process.Kill()
		runner.MessageBus = nil
		runner.natsSession = nil
	}
}
