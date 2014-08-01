package world

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net"
	"os"
	"os/exec"
	"path"
	"strconv"
	"time"

	"github.com/cloudfoundry-incubator/inigo/fake_cc"
	"github.com/cloudfoundry-incubator/inigo/loggregator_runner"
	wardenrunner "github.com/cloudfoundry-incubator/warden-linux/integration/runner"
	gorouterconfig "github.com/cloudfoundry/gorouter/config"
	"github.com/fraenkel/candiedyaml"
	"github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/tedsuo/ifrit"
	"github.com/tedsuo/ifrit/ginkgomon"
)

type BuiltExecutables map[string]string
type BuiltCircuses map[string]string

const CircusZipFilename = "some-circus.tgz"

type BuiltArtifacts struct {
	Executables BuiltExecutables
	Circuses    BuiltCircuses
}

type ComponentAddresses struct {
	NATS           string
	Etcd           string
	EtcdPeer       string
	LoggregatorIn  string
	LoggregatorOut string
	Executor       string
	Rep            string
	FakeCC         string
	FileServer     string
	Router         string
	TPS            string
	WardenLinux    string
}

type ComponentMaker struct {
	Artifacts BuiltArtifacts
	Addresses ComponentAddresses

	ExternalAddress string

	Stack string

	WardenBinPath    string
	WardenRootFSPath string
	WardenGraphPath  string
}

func (maker ComponentMaker) NATS(argv ...string) ifrit.Runner {
	host, port, err := net.SplitHostPort(maker.Addresses.NATS)
	Ω(err).ShouldNot(HaveOccurred())

	return &ginkgomon.Runner{
		Name:              "gnatsd",
		AnsiColorCode:     "30",
		StartCheck:        "gnatsd is ready",
		StartCheckTimeout: 5 * time.Second,
		Command: exec.Command(
			"gnatsd",
			append([]string{
				"--addr", host,
				"--port", port,
			}, argv...)...,
		),
	}
}

func (maker ComponentMaker) Etcd(argv ...string) ifrit.Runner {
	nodeName := fmt.Sprintf("etcd_%d", ginkgo.GinkgoParallelNode())
	dataDir := path.Join(os.TempDir(), nodeName)

	return &ginkgomon.Runner{
		Name:              "etcd",
		AnsiColorCode:     "30",
		StartCheck:        "leader changed",
		StartCheckTimeout: 5 * time.Second,
		Command: exec.Command(
			"etcd",
			append([]string{
				"-data-dir", dataDir,
				"-addr", maker.Addresses.Etcd,
				"-peer-addr", maker.Addresses.EtcdPeer,
				"-name", nodeName,
			}, argv...)...,
		),
		Cleanup: func() {
			err := os.RemoveAll(dataDir)
			Ω(err).ShouldNot(HaveOccurred())
		},
	}
}

func (maker ComponentMaker) WardenLinux(argv ...string) *wardenrunner.Runner {
	return wardenrunner.New(
		"tcp",
		maker.Addresses.WardenLinux,
		maker.Artifacts.Executables["warden-linux"],
		maker.WardenBinPath,
		maker.WardenRootFSPath,
		argv...,
	)
}

func (maker ComponentMaker) Executor(argv ...string) ifrit.Runner {
	return &ginkgomon.Runner{
		Name:              "exec",
		AnsiColorCode:     "31",
		StartCheck:        "executor.started",
		StartCheckTimeout: 5 * time.Second,
		Command: exec.Command(
			maker.Artifacts.Executables["exec"],
			append([]string{
				"-listenAddr", maker.Addresses.Executor,
				"-wardenNetwork", "tcp",
				"-wardenAddr", maker.Addresses.WardenLinux,
				"-loggregatorServer", maker.Addresses.LoggregatorIn,
				"-loggregatorSecret", "loggregator-secret",
				"-containerMaxCpuShares", "1024",
			}, argv...)...,
		),
	}
}

func (maker ComponentMaker) Rep(argv ...string) ifrit.Runner {
	return &ginkgomon.Runner{
		Name:              "rep",
		AnsiColorCode:     "32",
		StartCheck:        "rep.started",
		StartCheckTimeout: 5 * time.Second,
		Command: exec.Command(
			maker.Artifacts.Executables["rep"],
			append(
				[]string{
					"-stack", maker.Stack,
					"-lrpHost", maker.ExternalAddress,
					"-listenAddr", maker.Addresses.Rep,
					"-etcdCluster", "http://" + maker.Addresses.Etcd,
					"-natsAddresses", maker.Addresses.NATS,
					"-executorID", "the-executor-id-" + strconv.Itoa(ginkgo.GinkgoParallelNode()),
					"-executorURL", "http://" + maker.Addresses.Executor,
					"-heartbeatInterval", "1s",
				},
				argv...,
			)...,
		),
	}
}

func (maker ComponentMaker) Converger(argv ...string) ifrit.Runner {
	return &ginkgomon.Runner{
		Name:              "converger",
		AnsiColorCode:     "32",
		StartCheck:        "converger.started",
		StartCheckTimeout: 5 * time.Second,

		Command: exec.Command(
			maker.Artifacts.Executables["converger"],
			append([]string{
				"-etcdCluster", "http://" + maker.Addresses.Etcd,
			}, argv...)...,
		),
	}
}

func (maker ComponentMaker) Auctioneer(argv ...string) ifrit.Runner {
	return &ginkgomon.Runner{
		Name:          "auctioneer",
		AnsiColorCode: "33",
		StartCheck:    "auctioneer.started",
		Command: exec.Command(
			maker.Artifacts.Executables["auctioneer"],
			append([]string{
				"-etcdCluster", "http://" + maker.Addresses.Etcd,
				"-natsAddresses", maker.Addresses.NATS,

				// we limit this to prevent overwhelming numbers of auctioneer logs.  it
				// should not impact the behavior of the tests.
				"-maxRounds", "3",
			}, argv...)...,
		),
	}
}

func (maker ComponentMaker) RouteEmitter(argv ...string) ifrit.Runner {
	return &ginkgomon.Runner{
		Name:          "route-emitter",
		AnsiColorCode: "35",
		StartCheck:    "route-emitter.started",
		Command: exec.Command(
			maker.Artifacts.Executables["route-emitter"],
			append([]string{
				"-etcdCluster", "http://" + maker.Addresses.Etcd,
				"-natsAddresses", maker.Addresses.NATS,
			}, argv...)...,
		),
	}
}

func (maker ComponentMaker) TPS(argv ...string) ifrit.Runner {
	return &ginkgomon.Runner{
		Name:          "tps",
		AnsiColorCode: "36",
		StartCheck:    "tps.started",
		Command: exec.Command(
			maker.Artifacts.Executables["tps"],
			append([]string{
				"-etcdCluster", "http://" + maker.Addresses.Etcd,
				"-natsAddresses", maker.Addresses.NATS,
				"-listenAddr", maker.Addresses.TPS,
			}, argv...)...,
		),
	}
}

func (maker ComponentMaker) NsyncListener(argv ...string) ifrit.Runner {
	return &ginkgomon.Runner{
		Name:          "nsync-listener",
		AnsiColorCode: "37",
		StartCheck:    "nsync.listener.started",
		Command: exec.Command(
			maker.Artifacts.Executables["nsync-listener"],
			append([]string{
				"-etcdCluster", "http://" + maker.Addresses.Etcd,
				"-natsAddresses", maker.Addresses.NATS,
				"-repAddrRelativeToExecutor", maker.Addresses.Rep,
				"-circuses", fmt.Sprintf(`{"%s": "%s"}`, maker.Stack, CircusZipFilename),
			}, argv...)...,
		),
	}
}

func (maker ComponentMaker) FileServer(argv ...string) (ifrit.Runner, string) {
	servedFilesDir, err := ioutil.TempDir("", "file-server-files")
	Ω(err).ShouldNot(HaveOccurred())

	host, port, err := net.SplitHostPort(maker.Addresses.FileServer)
	Ω(err).ShouldNot(HaveOccurred())

	return &ginkgomon.Runner{
		Name:          "file-server",
		AnsiColorCode: "31",
		StartCheck:    "file-server.ready",
		Command: exec.Command(
			maker.Artifacts.Executables["file-server"],
			append([]string{
				"-address", host,
				"-port", port,
				"-etcdCluster", "http://" + maker.Addresses.Etcd,
				"-ccAddress", "http://" + maker.Addresses.FakeCC,
				"-ccJobPollingInterval", "100ms",
				"-ccUsername", fake_cc.CC_USERNAME,
				"-ccPassword", fake_cc.CC_PASSWORD,
				"-staticDirectory", servedFilesDir,
			}, argv...)...,
		),
		Cleanup: func() {
			err := os.RemoveAll(servedFilesDir)
			Ω(err).ShouldNot(HaveOccurred())
		},
	}, servedFilesDir
}

func (maker ComponentMaker) Router() ifrit.Runner {
	_, routerPort, err := net.SplitHostPort(maker.Addresses.Router)
	Ω(err).ShouldNot(HaveOccurred())

	routerPortInt, err := strconv.Atoi(routerPort)
	Ω(err).ShouldNot(HaveOccurred())

	natsHost, natsPort, err := net.SplitHostPort(maker.Addresses.NATS)
	Ω(err).ShouldNot(HaveOccurred())

	natsPortInt, err := strconv.Atoi(natsPort)
	Ω(err).ShouldNot(HaveOccurred())

	routerConfig := &gorouterconfig.Config{
		Port: uint16(routerPortInt),

		PruneStaleDropletsIntervalInSeconds: 5,
		DropletStaleThresholdInSeconds:      10,
		PublishActiveAppsIntervalInSeconds:  0,
		StartResponseDelayIntervalInSeconds: 1,

		Nats: []gorouterconfig.NatsConfig{
			{
				Host: natsHost,
				Port: uint16(natsPortInt),
			},
		},
		Logging: gorouterconfig.LoggingConfig{
			File:  "/dev/stdout",
			Level: "info",
		},
	}

	configFile, err := ioutil.TempFile(os.TempDir(), "router-config")
	Ω(err).ShouldNot(HaveOccurred())

	defer configFile.Close()

	err = candiedyaml.NewEncoder(configFile).Encode(routerConfig)
	Ω(err).ShouldNot(HaveOccurred())

	return &ginkgomon.Runner{
		Name:              "router",
		AnsiColorCode:     "32",
		StartCheck:        "router.started",
		StartCheckTimeout: 5 * time.Second, // it waits 1 second before listening. yep.
		Command: exec.Command(
			maker.Artifacts.Executables["router"],
			"-c", configFile.Name(),
		),
		Cleanup: func() {
			err := os.Remove(configFile.Name())
			Ω(err).ShouldNot(HaveOccurred())
		},
	}
}

func (maker ComponentMaker) Loggregator() ifrit.Runner {
	_, inPort, err := net.SplitHostPort(maker.Addresses.LoggregatorIn)
	Ω(err).ShouldNot(HaveOccurred())

	_, outPort, err := net.SplitHostPort(maker.Addresses.LoggregatorOut)
	Ω(err).ShouldNot(HaveOccurred())

	inPortInt, err := strconv.Atoi(inPort)
	Ω(err).ShouldNot(HaveOccurred())

	outPortInt, err := strconv.Atoi(outPort)
	Ω(err).ShouldNot(HaveOccurred())

	natsHost, natsPort, err := net.SplitHostPort(maker.Addresses.NATS)
	Ω(err).ShouldNot(HaveOccurred())

	natsPortInt, err := strconv.Atoi(natsPort)
	Ω(err).ShouldNot(HaveOccurred())

	loggregatorConfig := loggregator_runner.Config{
		IncomingPort:           inPortInt,
		OutgoingPort:           outPortInt,
		MaxRetainedLogMessages: 1000,
		SharedSecret:           "loggregator-secret",
		NatsHost:               natsHost,
		NatsPort:               natsPortInt,
	}

	configFile, err := ioutil.TempFile(os.TempDir(), "loggregator-config")
	Ω(err).ShouldNot(HaveOccurred())

	defer configFile.Close()

	err = json.NewEncoder(configFile).Encode(loggregatorConfig)
	Ω(err).ShouldNot(HaveOccurred())

	return &ginkgomon.Runner{
		Name:          "loggregator",
		AnsiColorCode: "33",
		StartCheck:    "Listening on port",
		Command: exec.Command(
			maker.Artifacts.Executables["loggregator"],
			"-config", configFile.Name(),
		),
		Cleanup: func() {
			err := os.Remove(configFile.Name())
			Ω(err).ShouldNot(HaveOccurred())
		},
	}
}

func (maker ComponentMaker) FakeCC() *fake_cc.FakeCC {
	return fake_cc.New(maker.Addresses.FakeCC)
}

func (maker ComponentMaker) Stager(argv ...string) ifrit.Runner {
	return &ginkgomon.Runner{
		Name:          "stager",
		AnsiColorCode: "34",
		StartCheck:    "Listening for staging requests!",
		Command: exec.Command(
			maker.Artifacts.Executables["stager"],
			append([]string{
				"-etcdCluster", "http://" + maker.Addresses.Etcd,
				"-natsAddresses", maker.Addresses.NATS,
				"-circuses", fmt.Sprintf(`{"%s": "%s"}`, maker.Stack, CircusZipFilename),
			}, argv...)...,
		),
	}
}
