package world

import (
	"fmt"
	"io/ioutil"
	"net"
	"os"
	"os/exec"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/cloudfoundry-incubator/candiedyaml"
	"github.com/cloudfoundry-incubator/consuladapter/consulrunner"
	"github.com/cloudfoundry-incubator/garden"
	gardenclient "github.com/cloudfoundry-incubator/garden/client"
	gardenconnection "github.com/cloudfoundry-incubator/garden/client/connection"
	"github.com/cloudfoundry-incubator/inigo/fake_cc"
	"github.com/cloudfoundry-incubator/inigo/gardenrunner"
	"github.com/cloudfoundry-incubator/receptor"
	gorouterconfig "github.com/cloudfoundry/gorouter/config"
	"github.com/cloudfoundry/gunk/diegonats"
	"github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/tedsuo/ifrit"
	"github.com/tedsuo/ifrit/ginkgomon"
	"golang.org/x/crypto/ssh"
)

type BuiltExecutables map[string]string
type BuiltLifecycles map[string]string

const LifecycleFilename = "some-lifecycle.tar.gz"

type BuiltArtifacts struct {
	Executables BuiltExecutables
	Lifecycles  BuiltLifecycles
}

type SSHKeys struct {
	HostKey       ssh.Signer
	HostKeyPem    string
	PrivateKeyPem string
	AuthorizedKey string
}

type SSLConfig struct {
	ServerCert string
	ServerKey  string
	ClientCert string
	ClientKey  string
	CACert     string
}

type ComponentAddresses struct {
	NATS                string
	Etcd                string
	EtcdPeer            string
	Consul              string
	BBS                 string
	Rep                 string
	FakeCC              string
	FileServer          string
	CCUploader          string
	Router              string
	TPSListener         string
	GardenLinux         string
	Receptor            string
	ReceptorTaskHandler string
	Stager              string
	NsyncListener       string
	Auctioneer          string
	SSHProxy            string
}

type ComponentMaker struct {
	Artifacts BuiltArtifacts
	Addresses ComponentAddresses

	ExternalAddress string

	PreloadedStackPathMap map[string]string

	GardenBinPath   string
	GardenGraphPath string

	SSHConfig SSHKeys
	SSL       SSLConfig
}

func (maker ComponentMaker) NATS(argv ...string) ifrit.Runner {
	host, port, err := net.SplitHostPort(maker.Addresses.NATS)
	Expect(err).NotTo(HaveOccurred())

	return ginkgomon.New(ginkgomon.Config{
		Name:              "gnatsd",
		AnsiColorCode:     "30m",
		StartCheck:        "gnatsd is ready",
		StartCheckTimeout: 10 * time.Second,
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
		StartCheckTimeout: 10 * time.Second,
		Command: exec.Command(
			"etcd",
			append([]string{
				"--name", nodeName,
				"--data-dir", dataDir,
				"--listen-client-urls", "https://" + maker.Addresses.Etcd,
				"--listen-peer-urls", "http://" + maker.Addresses.EtcdPeer,
				"--initial-cluster", nodeName + "=" + "http://" + maker.Addresses.EtcdPeer,
				"--initial-advertise-peer-urls", "http://" + maker.Addresses.EtcdPeer,
				"--initial-cluster-state", "new",
				"--advertise-client-urls", "https://" + maker.Addresses.Etcd,
				"--cert-file", maker.SSL.ServerCert,
				"--key-file", maker.SSL.ServerKey,
				"--ca-file", maker.SSL.CACert,
			}, argv...)...,
		),
		Cleanup: func() {
			err := os.RemoveAll(dataDir)
			Expect(err).NotTo(HaveOccurred())
		},
	})
}

func (maker ComponentMaker) Consul(argv ...string) ifrit.Runner {
	_, port, err := net.SplitHostPort(maker.Addresses.Consul)
	Expect(err).NotTo(HaveOccurred())
	httpPort, err := strconv.Atoi(port)
	Expect(err).NotTo(HaveOccurred())

	startingPort := httpPort - consulrunner.PortOffsetHTTP

	clusterRunner := consulrunner.NewClusterRunner(startingPort, 1, "http")
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

func (maker ComponentMaker) BBS(argv ...string) ifrit.Runner {
	return ginkgomon.New(ginkgomon.Config{
		Name:              "bbs",
		AnsiColorCode:     "32m",
		StartCheck:        "bbs.started",
		StartCheckTimeout: 10 * time.Second,
		Command: exec.Command(
			maker.Artifacts.Executables["bbs"],
			append([]string{
				"-address", maker.Addresses.BBS,
				"-auctioneerAddress", "http://" + maker.Addresses.Auctioneer,
				"-consulCluster", maker.ConsulCluster(),
				"-etcdCluster", maker.EtcdCluster(),
				"-etcdCertFile", maker.SSL.ClientCert,
				"-etcdKeyFile", maker.SSL.ClientKey,
				"-etcdCaFile", maker.SSL.CACert,
				"-logLevel", "debug",
			}, argv...)...,
		),
	})
}

func (maker ComponentMaker) Rep(argv ...string) *ginkgomon.Runner {
	return maker.RepN(0, argv...)
}

func (maker ComponentMaker) RepN(n int, argv ...string) *ginkgomon.Runner {
	host, portString, err := net.SplitHostPort(maker.Addresses.Rep)
	Expect(err).NotTo(HaveOccurred())
	port, err := strconv.Atoi(portString)
	Expect(err).NotTo(HaveOccurred())

	name := "rep-" + strconv.Itoa(n)

	tmpDir, err := ioutil.TempDir(os.TempDir(), "executor")
	Expect(err).NotTo(HaveOccurred())

	cachePath := path.Join(tmpDir, "cache")

	args := append(
		[]string{
			"-sessionName", name,
			"-rootFSProvider", "docker",
			"-etcdCluster", "https://" + maker.Addresses.Etcd,
			"-bbsAddress", fmt.Sprintf("http://%s", maker.Addresses.BBS),
			"-listenAddr", fmt.Sprintf("%s:%d", host, offsetPort(port, n)),
			"-cellID", "the-cell-id-" + strconv.Itoa(ginkgo.GinkgoParallelNode()) + "-" + strconv.Itoa(n),
			"-pollingInterval", "1s",
			"-evacuationPollingInterval", "1s",
			"-evacuationTimeout", "1s",
			"-lockTTL", "10s",
			"-lockRetryInterval", "1s",
			"-consulCluster", maker.ConsulCluster(),
			"-receptorTaskHandlerURL", "http://" + maker.Addresses.ReceptorTaskHandler,
			"-gardenNetwork", "tcp",
			"-gardenAddr", maker.Addresses.GardenLinux,
			"-containerMaxCpuShares", "1024",
			"-cachePath", cachePath,
			"-tempDir", tmpDir,
			"-logLevel", "debug",
			"-etcdCertFile", maker.SSL.ClientCert,
			"-etcdKeyFile", maker.SSL.ClientKey,
			"-etcdCaFile", maker.SSL.CACert,
		},
		argv...,
	)
	for stack, path := range maker.PreloadedStackPathMap {
		args = append(args, "-preloadedRootFS", fmt.Sprintf("%s:%s", stack, path))
	}

	return ginkgomon.New(ginkgomon.Config{
		Name:          name,
		AnsiColorCode: "33m",
		StartCheck:    `"` + name + `.services-bbs.presence.succeeded-setting-presence"`,
		// rep is not started until it can ping an executor; executor can take a
		// bit to start, so account for it
		StartCheckTimeout: 30 * time.Second,
		Command:           exec.Command(maker.Artifacts.Executables["rep"], args...),
		Cleanup: func() {
			os.RemoveAll(tmpDir)
		},
	})
}

func (maker ComponentMaker) Converger(argv ...string) ifrit.Runner {
	return ginkgomon.New(ginkgomon.Config{
		Name:              "converger",
		AnsiColorCode:     "34m",
		StartCheck:        `"converger.started"`,
		StartCheckTimeout: 15 * time.Second,

		Command: exec.Command(
			maker.Artifacts.Executables["converger"],
			append([]string{
				"-etcdCluster", maker.EtcdCluster(),
				"-bbsAddress", fmt.Sprintf("http://%s", maker.Addresses.BBS),
				"-lockTTL", "10s",
				"-lockRetryInterval", "1s",
				"-consulCluster", maker.ConsulCluster(),
				"-receptorTaskHandlerURL", "http://" + maker.Addresses.ReceptorTaskHandler,
				"-logLevel", "debug",
				"-etcdCertFile", maker.SSL.ClientCert,
				"-etcdKeyFile", maker.SSL.ClientKey,
				"-etcdCaFile", maker.SSL.CACert,
			}, argv...)...,
		),
	})
}

func (maker ComponentMaker) Auctioneer(argv ...string) ifrit.Runner {
	return ginkgomon.New(ginkgomon.Config{
		Name:              "auctioneer",
		AnsiColorCode:     "35m",
		StartCheck:        `"auctioneer.started"`,
		StartCheckTimeout: 10 * time.Second,
		Command: exec.Command(
			maker.Artifacts.Executables["auctioneer"],
			append([]string{
				"-bbsAddress", fmt.Sprintf("http://%s", maker.Addresses.BBS),
				"-etcdCluster", maker.EtcdCluster(),
				"-listenAddr", maker.Addresses.Auctioneer,
				"-lockRetryInterval", "1s",
				"-consulCluster", maker.ConsulCluster(),
				"-receptorTaskHandlerURL", "http://" + maker.Addresses.ReceptorTaskHandler,
				"-logLevel", "debug",
				"-etcdCertFile", maker.SSL.ClientCert,
				"-etcdKeyFile", maker.SSL.ClientKey,
				"-etcdCaFile", maker.SSL.CACert,
			}, argv...)...,
		),
	})
}

func (maker ComponentMaker) RouteEmitter(argv ...string) ifrit.Runner {
	return ginkgomon.New(ginkgomon.Config{
		Name:              "route-emitter",
		AnsiColorCode:     "36m",
		StartCheck:        `"route-emitter.started"`,
		StartCheckTimeout: 10 * time.Second,
		Command: exec.Command(
			maker.Artifacts.Executables["route-emitter"],
			append([]string{
				"-natsAddresses", maker.Addresses.NATS,
				"-diegoAPIURL", "http://" + maker.Addresses.Receptor,
				"-lockRetryInterval", "1s",
				"-consulCluster", maker.ConsulCluster(),
				"-logLevel", "debug",
			}, argv...)...,
		),
	})
}

func (maker ComponentMaker) TPSListener(argv ...string) ifrit.Runner {
	return ginkgomon.New(ginkgomon.Config{
		Name:              "tps-listener",
		AnsiColorCode:     "37m",
		StartCheck:        `"tps-listener.started"`,
		StartCheckTimeout: 10 * time.Second,
		Command: exec.Command(
			maker.Artifacts.Executables["tps-listener"],
			append([]string{
				"-diegoAPIURL", "http://" + maker.Addresses.Receptor,
				"-listenAddr", maker.Addresses.TPSListener,
				"-logLevel", "debug",
			}, argv...)...,
		),
	})
}

func (maker ComponentMaker) NsyncListener(argv ...string) ifrit.Runner {
	address := maker.Addresses.NsyncListener
	port, err := strconv.Atoi(strings.Split(address, ":")[1])
	Expect(err).NotTo(HaveOccurred())

	return ginkgomon.New(ginkgomon.Config{
		Name:              "nsync-listener",
		AnsiColorCode:     "90m",
		StartCheck:        `"nsync.listener.started"`,
		StartCheckTimeout: 10 * time.Second,
		Command: exec.Command(
			maker.Artifacts.Executables["nsync-listener"],
			append(maker.appendLifecycleArgs([]string{
				"-diegoAPIURL", "http://" + maker.Addresses.Receptor,
				"-nsyncURL", fmt.Sprintf("http://127.0.0.1:%d", port),
				"-fileServerURL", "http://" + maker.Addresses.FileServer,
				"-logLevel", "debug",
			}), argv...)...,
		),
	})
}

func (maker ComponentMaker) CCUploader(argv ...string) ifrit.Runner {
	return ginkgomon.New(ginkgomon.Config{
		Name:              "cc-uploader",
		AnsiColorCode:     "91m",
		StartCheck:        `"cc-uploader.ready"`,
		StartCheckTimeout: 10 * time.Second,
		Command: exec.Command(
			maker.Artifacts.Executables["cc-uploader"],
			append([]string{
				"-address", maker.Addresses.CCUploader,
				"-ccJobPollingInterval", "100ms",
				"-logLevel", "debug",
			}, argv...)...,
		),
	})
}

func (maker ComponentMaker) FileServer(argv ...string) (ifrit.Runner, string) {
	servedFilesDir, err := ioutil.TempDir("", "file-server-files")
	Expect(err).NotTo(HaveOccurred())

	return ginkgomon.New(ginkgomon.Config{
		Name:              "file-server",
		AnsiColorCode:     "92m",
		StartCheck:        `"file-server.ready"`,
		StartCheckTimeout: 10 * time.Second,
		Command: exec.Command(
			maker.Artifacts.Executables["file-server"],
			append([]string{
				"-address", maker.Addresses.FileServer,
				"-staticDirectory", servedFilesDir,
				"-logLevel", "debug",
			}, argv...)...,
		),
		Cleanup: func() {
			err := os.RemoveAll(servedFilesDir)
			Expect(err).NotTo(HaveOccurred())
		},
	}), servedFilesDir
}

func (maker ComponentMaker) Router() ifrit.Runner {
	_, routerPort, err := net.SplitHostPort(maker.Addresses.Router)
	Expect(err).NotTo(HaveOccurred())

	routerPortInt, err := strconv.Atoi(routerPort)
	Expect(err).NotTo(HaveOccurred())

	natsHost, natsPort, err := net.SplitHostPort(maker.Addresses.NATS)
	Expect(err).NotTo(HaveOccurred())

	natsPortInt, err := strconv.Atoi(natsPort)
	Expect(err).NotTo(HaveOccurred())

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
	Expect(err).NotTo(HaveOccurred())

	defer configFile.Close()

	err = candiedyaml.NewEncoder(configFile).Encode(routerConfig)
	Expect(err).NotTo(HaveOccurred())

	return ginkgomon.New(ginkgomon.Config{
		Name:              "router",
		AnsiColorCode:     "93m",
		StartCheck:        "router.started",
		StartCheckTimeout: 10 * time.Second, // it waits 1 second before listening. yep.
		Command: exec.Command(
			maker.Artifacts.Executables["router"],
			"-c", configFile.Name(),
		),
		Cleanup: func() {
			err := os.Remove(configFile.Name())
			Expect(err).NotTo(HaveOccurred())
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
	Expect(err).NotTo(HaveOccurred())

	return ginkgomon.New(ginkgomon.Config{
		Name:              "stager",
		AnsiColorCode:     "94m",
		StartCheck:        "Listening for staging requests!",
		StartCheckTimeout: 10 * time.Second,
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
				"-ccUploaderURL", "http://" + maker.Addresses.CCUploader,
				"-logLevel", "debug",
			}), argv...)...,
		),
	})
}

func (maker ComponentMaker) Receptor(argv ...string) ifrit.Runner {
	return ginkgomon.New(ginkgomon.Config{
		Name:              "receptor",
		AnsiColorCode:     "95m",
		StartCheck:        "receptor.started",
		StartCheckTimeout: 10 * time.Second,
		Command: exec.Command(
			maker.Artifacts.Executables["receptor"],
			append([]string{
				"-address", maker.Addresses.Receptor,
				"-bbsAddress", fmt.Sprintf("http://%s", maker.Addresses.BBS),
				"-taskHandlerAddress", maker.Addresses.ReceptorTaskHandler,
				"-etcdCluster", maker.EtcdCluster(),
				"-consulCluster", maker.ConsulCluster(),
				"-logLevel", "debug",
				"-etcdCertFile", maker.SSL.ClientCert,
				"-etcdKeyFile", maker.SSL.ClientKey,
				"-etcdCaFile", maker.SSL.CACert,
			}, argv...)...,
		),
	})
}

func (maker ComponentMaker) SSHProxy(argv ...string) ifrit.Runner {
	return ginkgomon.New(ginkgomon.Config{
		Name:              "ssh-proxy",
		AnsiColorCode:     "96m",
		StartCheck:        "ssh-proxy.started",
		StartCheckTimeout: 10 * time.Second,
		Command: exec.Command(
			maker.Artifacts.Executables["ssh-proxy"],
			append([]string{
				"-address", maker.Addresses.SSHProxy,
				"-hostKey", maker.SSHConfig.HostKeyPem,
				"-diegoAPIURL", "http://" + maker.Addresses.Receptor,
				"-logLevel", "debug",
				"-enableDiegoAuth",
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
	Expect(maker.PreloadedStackPathMap).NotTo(BeEmpty())

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
	Expect(err).NotTo(HaveOccurred())

	return client
}

func (maker ComponentMaker) GardenClient() garden.Client {
	return gardenclient.New(gardenconnection.New("tcp", maker.Addresses.GardenLinux))
}

func (maker ComponentMaker) ReceptorClient() receptor.Client {
	return receptor.NewClient("http://" + maker.Addresses.Receptor)
}

func (maker ComponentMaker) ConsulCluster() string {
	return "http://" + maker.Addresses.Consul
}

func (maker ComponentMaker) EtcdCluster() string {
	return "https://" + maker.Addresses.Etcd
}

// offsetPort retuns a new port offest by a given number in such a way
// that it does not interfere with the ginkgo parallel node offest in the base port.
func offsetPort(basePort, offset int) int {
	return basePort + (10 * offset)
}
