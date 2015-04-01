package world

import (
	"fmt"
	"io/ioutil"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/cloudfoundry-incubator/candiedyaml"
	"github.com/cloudfoundry-incubator/consuladapter"
	"github.com/cloudfoundry-incubator/executor"
	executorclient "github.com/cloudfoundry-incubator/executor/http/client"
	"github.com/cloudfoundry-incubator/garden"
	gardenrunner "github.com/cloudfoundry-incubator/garden-linux/integration/runner"
	gardenclient "github.com/cloudfoundry-incubator/garden/client"
	gardenconnection "github.com/cloudfoundry-incubator/garden/client/connection"
	"github.com/cloudfoundry-incubator/inigo/fake_cc"
	"github.com/cloudfoundry-incubator/receptor"
	gorouterconfig "github.com/cloudfoundry/gorouter/config"
	"github.com/cloudfoundry/gunk/diegonats"
	"github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/tedsuo/ifrit"
	"github.com/tedsuo/ifrit/ginkgomon"
)

type BuiltExecutables map[string]string
type BuiltLifecycles map[string]string

const LifecycleFilename = "some-lifecycle.tar.gz"

type BuiltArtifacts struct {
	Executables BuiltExecutables
	Lifecycles  BuiltLifecycles
}

type ComponentAddresses struct {
	NATS                string
	Etcd                string
	EtcdPeer            string
	Consul              string
	Executor            string
	Rep                 string
	FakeCC              string
	FileServer          string
	Router              string
	TPS                 string
	GardenLinux         string
	Receptor            string
	ReceptorTaskHandler string
	Stager              string
	NsyncListener       string
	Auctioneer          string
}

type ComponentMaker struct {
	Artifacts BuiltArtifacts
	Addresses ComponentAddresses

	ExternalAddress string

	PreloadedStackPathMap map[string]string

	GardenBinPath   string
	GardenGraphPath string
}

func (maker ComponentMaker) NATS(argv ...string) ifrit.Runner {
	host, port, err := net.SplitHostPort(maker.Addresses.NATS)
	Ω(err).ShouldNot(HaveOccurred())

	return ginkgomon.New(ginkgomon.Config{
		Name:              "gnatsd",
		AnsiColorCode:     "30m",
		StartCheck:        "gnatsd is ready",
		StartCheckTimeout: 5 * time.Second,
		Command: exec.Command(
			"gnatsd",
			append([]string{
				"--addr", host,
				"--port", port,
			}, argv...)...,
		),
	})
}

func (maker ComponentMaker) Etcd(argv ...string) ifrit.Runner {
	nodeName := fmt.Sprintf("etcd_%d", ginkgo.GinkgoParallelNode())
	dataDir := path.Join(os.TempDir(), nodeName)

	return ginkgomon.New(ginkgomon.Config{
		Name:              "etcd",
		AnsiColorCode:     "31m",
		StartCheck:        "etcdserver: published",
		StartCheckTimeout: 5 * time.Second,
		Command: exec.Command(
			"etcd",
			append([]string{
				"--name", nodeName,
				"--data-dir", dataDir,
				"--listen-client-urls", "http://" + maker.Addresses.Etcd,
				"--listen-peer-urls", "http://" + maker.Addresses.EtcdPeer,
				"--initial-cluster", nodeName + "=" + "http://" + maker.Addresses.EtcdPeer,
				"--initial-advertise-peer-urls", "http://" + maker.Addresses.EtcdPeer,
				"--initial-cluster-state", "new",
				"--advertise-client-urls", "http://" + maker.Addresses.Etcd,
			}, argv...)...,
		),
		Cleanup: func() {
			err := os.RemoveAll(dataDir)
			Ω(err).ShouldNot(HaveOccurred())
		},
	})
}

func (maker ComponentMaker) Consul(argv ...string) ifrit.Runner {
	_, port, err := net.SplitHostPort(maker.Addresses.Consul)
	Ω(err).ShouldNot(HaveOccurred())
	httpPort, err := strconv.Atoi(port)
	Ω(err).ShouldNot(HaveOccurred())

	startingPort := httpPort - consuladapter.PortOffsetHTTP

	clusterRunner := consuladapter.NewClusterRunner(startingPort, 1, "http")
	return ifrit.RunFunc(func(signals <-chan os.Signal, ready chan<- struct{}) error {
		done := make(chan struct{})
		go func() {
			clusterRunner.Start()
			close(done)
		}()

		Eventually(done, 10).Should(BeClosed())

		close(ready)

		select {
		case <-signals:
			clusterRunner.Stop()
		}

		return nil
	})
}

func (maker ComponentMaker) GardenLinux(argv ...string) *gardenrunner.Runner {
	return gardenrunner.New(
		"tcp",
		maker.Addresses.GardenLinux,
		maker.Artifacts.Executables["garden-linux"],
		maker.GardenBinPath,
		maker.PreloadedStackPathMap[maker.DefaultStack()],
		maker.GardenGraphPath,
		argv...,
	)
}

func (maker ComponentMaker) Executor(argv ...string) *ginkgomon.Runner {
	tmpDir, err := ioutil.TempDir(os.TempDir(), "executor")
	Ω(err).ShouldNot(HaveOccurred())

	cachePath := path.Join(tmpDir, "cache")

	return ginkgomon.New(ginkgomon.Config{
		Name:          "executor",
		AnsiColorCode: "91m",
		StartCheck:    `"executor.started"`,
		// executor may destroy containers on start, which can take a bit
		StartCheckTimeout: 30 * time.Second,
		Command: exec.Command(
			maker.Artifacts.Executables["exec"],
			append([]string{
				"-listenAddr", maker.Addresses.Executor,
				"-gardenNetwork", "tcp",
				"-gardenAddr", maker.Addresses.GardenLinux,
				"-containerMaxCpuShares", "1024",
				"-cachePath", cachePath,
				"-tempDir", tmpDir,
			}, argv...)...,
		),
		Cleanup: func() {
			os.RemoveAll(tmpDir)
		},
	})
}

func (maker ComponentMaker) Rep(argv ...string) *ginkgomon.Runner {
	args := append(
		[]string{
			"-rootFSProvider", "docker",
			"-etcdCluster", "http://" + maker.Addresses.Etcd,
			"-listenAddr", maker.Addresses.Rep,
			"-cellID", "the-cell-id-" + strconv.Itoa(ginkgo.GinkgoParallelNode()),
			"-executorURL", "http://" + maker.Addresses.Executor,
			"-pollingInterval", "1s",
			"-evacuationPollingInterval", "1s",
			"-evacuationTimeout", "1s",
			"-lockTTL", "10s",
			"-heartbeatRetryInterval", "1s",
			"-consulCluster", maker.ConsulCluster(),
			"-receptorTaskHandlerURL", "http://" + maker.Addresses.ReceptorTaskHandler,
		},
		argv...,
	)
	for stack, path := range maker.PreloadedStackPathMap {
		args = append(args, "-preloadedRootFS", fmt.Sprintf("%s:%s", stack, path))
	}

	return ginkgomon.New(ginkgomon.Config{
		Name:          "rep",
		AnsiColorCode: "92m",
		StartCheck:    `"rep.started"`,
		// rep is not started until it can ping an executor; executor can take a
		// bit to start, so account for it
		StartCheckTimeout: 30 * time.Second,
		Command:           exec.Command(maker.Artifacts.Executables["rep"], args...),
	})
}

func (maker ComponentMaker) Converger(argv ...string) ifrit.Runner {
	return ginkgomon.New(ginkgomon.Config{
		Name:              "converger",
		AnsiColorCode:     "93m",
		StartCheck:        `"converger.started"`,
		StartCheckTimeout: 5 * time.Second,

		Command: exec.Command(
			maker.Artifacts.Executables["converger"],
			append([]string{
				"-etcdCluster", maker.EtcdCluster(),
				"-lockTTL", "10s",
				"-heartbeatRetryInterval", "1s",
				"-consulCluster", maker.ConsulCluster(),
				"-receptorTaskHandlerURL", "http://" + maker.Addresses.ReceptorTaskHandler,
			}, argv...)...,
		),
	})
}

func (maker ComponentMaker) Auctioneer(argv ...string) ifrit.Runner {
	return ginkgomon.New(ginkgomon.Config{
		Name:              "auctioneer",
		AnsiColorCode:     "94m",
		StartCheck:        `"auctioneer.started"`,
		StartCheckTimeout: 5 * time.Second,
		Command: exec.Command(
			maker.Artifacts.Executables["auctioneer"],
			append([]string{
				"-etcdCluster", maker.EtcdCluster(),
				"-listenAddr", maker.Addresses.Auctioneer,
				"-heartbeatRetryInterval", "1s",
				"-consulCluster", maker.ConsulCluster(),
				"-receptorTaskHandlerURL", "http://" + maker.Addresses.ReceptorTaskHandler,
			}, argv...)...,
		),
	})
}

func (maker ComponentMaker) RouteEmitter(argv ...string) ifrit.Runner {
	return ginkgomon.New(ginkgomon.Config{
		Name:              "route-emitter",
		AnsiColorCode:     "95m",
		StartCheck:        `"route-emitter.started"`,
		StartCheckTimeout: 5 * time.Second,
		Command: exec.Command(
			maker.Artifacts.Executables["route-emitter"],
			append([]string{
				"-natsAddresses", maker.Addresses.NATS,
				"-diegoAPIURL", "http://" + maker.Addresses.Receptor,
				"-heartbeatRetryInterval", "1s",
				"-consulCluster", maker.ConsulCluster(),
			}, argv...)...,
		),
	})
}

func (maker ComponentMaker) TPS(argv ...string) ifrit.Runner {
	return ginkgomon.New(ginkgomon.Config{
		Name:              "tps",
		AnsiColorCode:     "96m",
		StartCheck:        `"tps.started"`,
		StartCheckTimeout: 5 * time.Second,
		Command: exec.Command(
			maker.Artifacts.Executables["tps"],
			append([]string{
				"-diegoAPIURL", "http://" + maker.Addresses.Receptor,
				"-listenAddr", maker.Addresses.TPS,
				"-ccBaseURL", "http://" + maker.Addresses.FakeCC,
				"-ccUsername", fake_cc.CC_USERNAME,
				"-ccPassword", fake_cc.CC_PASSWORD,
			}, argv...)...,
		),
	})
}

func (maker ComponentMaker) NsyncListener(argv ...string) ifrit.Runner {
	address := maker.Addresses.NsyncListener
	port, err := strconv.Atoi(strings.Split(address, ":")[1])
	Ω(err).ShouldNot(HaveOccurred())

	return ginkgomon.New(ginkgomon.Config{
		Name:              "nsync-listener",
		AnsiColorCode:     "97m",
		StartCheck:        `"nsync.listener.started"`,
		StartCheckTimeout: 5 * time.Second,
		Command: exec.Command(
			maker.Artifacts.Executables["nsync-listener"],
			append(maker.appendLifecycleArgs([]string{
				"-diegoAPIURL", "http://" + maker.Addresses.Receptor,
				"-nsyncURL", fmt.Sprintf("http://127.0.0.1:%d", port),
				"-fileServerURL", "http://" + maker.Addresses.FileServer,
			}), argv...)...,
		),
	})
}

func (maker ComponentMaker) FileServer(argv ...string) (ifrit.Runner, string) {
	servedFilesDir, err := ioutil.TempDir("", "file-server-files")
	Ω(err).ShouldNot(HaveOccurred())

	return ginkgomon.New(ginkgomon.Config{
		Name:              "file-server",
		AnsiColorCode:     "90m",
		StartCheck:        `"file-server.ready"`,
		StartCheckTimeout: 5 * time.Second,
		Command: exec.Command(
			maker.Artifacts.Executables["file-server"],
			append([]string{
				"-address", maker.Addresses.FileServer,
				"-ccJobPollingInterval", "100ms",
				"-staticDirectory", servedFilesDir,
			}, argv...)...,
		),
		Cleanup: func() {
			err := os.RemoveAll(servedFilesDir)
			Ω(err).ShouldNot(HaveOccurred())
		},
	}), servedFilesDir
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
			File:          "/dev/stdout",
			Level:         "info",
			MetronAddress: "127.0.0.1:65534", // nonsense to make dropsonde happy
		},
	}

	configFile, err := ioutil.TempFile(os.TempDir(), "router-config")
	Ω(err).ShouldNot(HaveOccurred())

	defer configFile.Close()

	err = candiedyaml.NewEncoder(configFile).Encode(routerConfig)
	Ω(err).ShouldNot(HaveOccurred())

	return ginkgomon.New(ginkgomon.Config{
		Name:              "router",
		AnsiColorCode:     "32m",
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
	})
}

func (maker ComponentMaker) FakeCC() *fake_cc.FakeCC {
	return fake_cc.New(maker.Addresses.FakeCC)
}

func (maker ComponentMaker) Stager(argv ...string) ifrit.Runner {
	return maker.StagerN(0, argv...)
}

func (maker ComponentMaker) StagerN(portOffset int, argv ...string) ifrit.Runner {
	address := maker.Addresses.Stager
	port, err := strconv.Atoi(strings.Split(address, ":")[1])
	Ω(err).ShouldNot(HaveOccurred())

	return ginkgomon.New(ginkgomon.Config{
		Name:              "stager",
		AnsiColorCode:     "94m",
		StartCheck:        "Listening for staging requests!",
		StartCheckTimeout: 5 * time.Second,
		Command: exec.Command(
			maker.Artifacts.Executables["stager"],
			append(maker.appendLifecycleArgs([]string{
				"-ccBaseURL", "http://" + maker.Addresses.FakeCC,
				"-ccUsername", fake_cc.CC_USERNAME,
				"-ccPassword", fake_cc.CC_PASSWORD,
				"-dockerStagingStack", maker.DefaultStack(),
				"-diegoAPIURL", "http://" + maker.Addresses.Receptor,
				"-stagerURL", fmt.Sprintf("http://127.0.0.1:%d", offsetPort(port, portOffset)),
				"-fileServerURL", "http://" + maker.Addresses.FileServer,
			}), argv...)...,
		),
	})
}

func (maker ComponentMaker) Receptor(argv ...string) ifrit.Runner {
	return ginkgomon.New(ginkgomon.Config{
		Name:              "receptor",
		AnsiColorCode:     "37m",
		StartCheck:        "started",
		StartCheckTimeout: 5 * time.Second,
		Command: exec.Command(
			maker.Artifacts.Executables["receptor"],
			append([]string{
				"-address", maker.Addresses.Receptor,
				"-taskHandlerAddress", maker.Addresses.ReceptorTaskHandler,
				"-etcdCluster", maker.EtcdCluster(),
				"-heartbeatRetryInterval", "1s",
				"-consulCluster", maker.ConsulCluster(),
			}, argv...)...,
		),
	})
}

func (maker ComponentMaker) appendLifecycleArgs(args []string) []string {
	for stack, _ := range maker.PreloadedStackPathMap {
		args = append(args, "-lifecycle", fmt.Sprintf("buildpack/%s:%s", stack, LifecycleFilename))
	}

	return args
}

func (maker ComponentMaker) DefaultStack() string {
	Ω(maker.PreloadedStackPathMap).ShouldNot(BeEmpty())

	var defaultStack string
	for stack, _ := range maker.PreloadedStackPathMap {
		defaultStack = stack
		break
	}

	return defaultStack
}

func (maker ComponentMaker) NATSClient() diegonats.NATSClient {
	client := diegonats.NewClient()

	_, err := client.Connect([]string{"nats://" + maker.Addresses.NATS})
	Ω(err).ShouldNot(HaveOccurred())

	return client
}

func (maker ComponentMaker) GardenClient() garden.Client {
	return gardenclient.New(gardenconnection.New("tcp", maker.Addresses.GardenLinux))
}

func (maker ComponentMaker) ExecutorClient() executor.Client {
	return executorclient.New(http.DefaultClient, http.DefaultClient, "http://"+maker.Addresses.Executor)
}

func (maker ComponentMaker) ReceptorClient() receptor.Client {
	return receptor.NewClient("http://" + maker.Addresses.Receptor)
}

func (maker ComponentMaker) ConsulCluster() string {
	return "http://" + maker.Addresses.Consul
}

func (maker ComponentMaker) EtcdCluster() string {
	return "http://" + maker.Addresses.Etcd
}

// offsetPort retuns a new port offest by a given number in such a way
// that it does not interfere with the ginkgo parallel node offest in the base port.
func offsetPort(basePort, offset int) int {
	return basePort + (10 * offset)
}
