package helpers

import (
	"fmt"

	"github.com/cloudfoundry-incubator/receptor"
	"github.com/cloudfoundry-incubator/route-emitter/cfroutes"
	"github.com/cloudfoundry-incubator/runtime-schema/models"
	. "github.com/onsi/gomega"
)

const defaultDomain = "inigo"

var defaultPreloadedRootFS = "preloaded:" + PreloadedStacks[0]
var SecondaryPreloadedRootFS = "preloaded:" + PreloadedStacks[1]

const BogusPreloadedRootFS = "preloaded:bogus-rootfs"
const dockerRootFS = "docker:///cloudfoundry/diego-docker-app#latest"

const DefaultHost = "lrp-route"

var defaultRoutes = cfroutes.CFRoutes{{Hostnames: []string{DefaultHost}, Port: 8080}}.RoutingInfo()
var defaultPorts = []uint16{8080}

var defaultSetupFunc = func() models.Action {
	return &models.DownloadAction{
		From: fmt.Sprintf("http://%s/v1/static/%s", addresses.FileServer, "lrp.zip"),
		To:   ".",
	}
}
var defaultAction = &models.RunAction{
	Path: "bash",
	Args: []string{"server.sh"},
	Env:  []models.EnvironmentVariable{{"PORT", "8080"}},
}
var defaultMonitor = &models.RunAction{
	Path: "true",
}

func UpsertInigoDomain(receptorClient receptor.Client) {
	err := receptorClient.UpsertDomain(defaultDomain, 0)
	Ω(err).ShouldNot(HaveOccurred())
}

func DefaultLRPCreateRequest(processGuid, logGuid string, numInstances int) receptor.DesiredLRPCreateRequest {
	return receptor.DesiredLRPCreateRequest{
		ProcessGuid: processGuid,
		Domain:      defaultDomain,
		RootFS:      defaultPreloadedRootFS,
		Instances:   numInstances,

		LogGuid: logGuid,

		Routes: defaultRoutes,
		Ports:  defaultPorts,

		Setup:   defaultSetupFunc(),
		Action:  defaultAction,
		Monitor: defaultMonitor,
	}
}

func LRPCreateRequestWithRootFS(processGuid, rootfs string) receptor.DesiredLRPCreateRequest {
	return receptor.DesiredLRPCreateRequest{
		ProcessGuid: processGuid,
		Domain:      defaultDomain,
		RootFS:      rootfs,
		Instances:   1,

		Routes: defaultRoutes,
		Ports:  defaultPorts,

		Setup:   defaultSetupFunc(),
		Action:  defaultAction,
		Monitor: defaultMonitor,
	}
}

func DockerLRPCreateRequest(processGuid string) receptor.DesiredLRPCreateRequest {
	return receptor.DesiredLRPCreateRequest{
		ProcessGuid: processGuid,
		Domain:      defaultDomain,
		RootFS:      dockerRootFS,
		Instances:   1,

		Routes: defaultRoutes,
		Ports:  defaultPorts,

		Action: &models.RunAction{
			Path: "/myapp/dockerapp",
			Env:  []models.EnvironmentVariable{{"PORT", "8080"}},
		},
		Monitor: defaultMonitor,
	}
}

func CrashingLRPCreateRequest(processGuid string) receptor.DesiredLRPCreateRequest {
	return receptor.DesiredLRPCreateRequest{
		ProcessGuid: processGuid,
		Domain:      defaultDomain,
		RootFS:      defaultPreloadedRootFS,
		Instances:   1,

		Action: &models.RunAction{Path: "false"},
	}
}

func LightweightLRPCreateRequest(processGuid string) receptor.DesiredLRPCreateRequest {
	return receptor.DesiredLRPCreateRequest{
		ProcessGuid: processGuid,
		Domain:      defaultDomain,
		RootFS:      defaultPreloadedRootFS,
		Instances:   1,

		MemoryMB: 128,
		DiskMB:   1024,

		Ports: defaultPorts,

		Action: &models.RunAction{
			Path: "sh",
			Args: []string{
				"-c",
				"while true; do sleep 1; done",
			},
		},
		Monitor: &models.RunAction{
			Path: "sh",
			Args: []string{"-c", "echo all good"},
		},
	}
}

func PrivilegedLRPCreateRequest(processGuid string) receptor.DesiredLRPCreateRequest {
	return receptor.DesiredLRPCreateRequest{
		ProcessGuid: processGuid,
		Domain:      defaultDomain,
		RootFS:      defaultPreloadedRootFS,
		Instances:   1,

		Routes: defaultRoutes,
		Ports:  defaultPorts,

		Action: &models.RunAction{
			Path: "bash",
			// always run as root; tests change task-level privileged
			Privileged: true,
			Args: []string{
				"-c",
				`
						mkfifo request

						while true; do
						{
							read < request

							status="200 OK"
							if ! echo h > /proc/sysrq-trigger; then
								status="500 Internal Server Error"
							fi

						  echo -n -e "HTTP/1.1 ${status}\r\n"
						  echo -n -e "Content-Length: 0\r\n\r\n"
						} | nc -l 0.0.0.0 8080 > request;
						done
						`,
			},
		},
	}
}

func TaskCreateRequest(taskGuid string, action models.Action) receptor.TaskCreateRequest {
	return taskCreateRequest(taskGuid, defaultPreloadedRootFS, action, 0, 0)
}

func TaskCreateRequestWithMemory(taskGuid string, action models.Action, memoryMB int) receptor.TaskCreateRequest {
	return taskCreateRequest(taskGuid, defaultPreloadedRootFS, action, memoryMB, 0)
}

func TaskCreateRequestWithRootFS(taskGuid, rootfs string, action models.Action) receptor.TaskCreateRequest {
	return taskCreateRequest(taskGuid, rootfs, action, 0, 0)
}

func TaskCreateRequestWithMemoryAndDisk(taskGuid string, action models.Action, memoryMB, diskMB int) receptor.TaskCreateRequest {
	return taskCreateRequest(taskGuid, defaultPreloadedRootFS, action, memoryMB, diskMB)
}

func taskCreateRequest(taskGuid, rootFS string, action models.Action, memoryMB, diskMB int) receptor.TaskCreateRequest {
	return receptor.TaskCreateRequest{
		TaskGuid: taskGuid,
		Domain:   defaultDomain,
		RootFS:   rootFS,
		MemoryMB: memoryMB,
		DiskMB:   diskMB,
		Action:   action,
	}
}
