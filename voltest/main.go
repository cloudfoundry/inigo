package main

import (
	"fmt"
	"log"
	"os"

	"github.com/cloudfoundry-incubator/receptor"
	"github.com/cloudfoundry-incubator/route-emitter/cfroutes"
	"github.com/cloudfoundry-incubator/runtime-schema/models"
)

var (
	user   = "user"
	pass   = "pass"
	target = "54.175.42.126.xip.io"

	processGuid = "dolt"
	volumeGuid  = "doltdata"
	domain      = "lattice"
	stack       = "lucid64"
	rootfs      = "docker:///busybox"
)

func main() {
	cmd := os.Args[1]
	log.Println("Performing", cmd)
	switch cmd {
	case "app":
		createApp()
	case "vols":
		createVolumes()
	case "ls":
		listResources()
	}
	log.Println("Complete")
}

func listResources() {
	c := newClient()

	log.Println("fetching volumes")
	vols, err := c.VolumesByVolumeSetGuid(volumeGuid)
	if err != nil {
		log.Printf("Can't fetch volumes:%s\n", err)
	} else {
		for i := range vols {
			log.Printf("%#v\n", vols[i])
		}
	}

	log.Println("fetching LRPs")
	lrps, err := c.ActualLRPsByProcessGuid(processGuid)
	if err != nil {
		log.Println("Can't fetch lrps:", err.Error())
	} else {
		for i := range lrps {
			log.Printf("%#v\n", lrps[i])
		}
	}
}

func createVolumes() {
	volSet := receptor.VolumeSetCreateRequest{
		VolumeSetGuid:    volumeGuid,
		Instances:        1,
		Stack:            stack,
		SizeMB:           10,
		ReservedMemoryMB: 10,
	}

	c := newClient()

	log.Println("Creating Volume Set...")
	err := c.CreateVolumeSet(volSet)
	logError(err)
}

func createApp() {
	volMount := receptor.VolumeSetAttachment{
		VolumeSetGuid: volumeGuid,
		Path:          "/data",
	}

	lrp := receptor.DesiredLRPCreateRequest{
		ProcessGuid: processGuid,
		Domain:      domain,
		RootFSPath:  rootfs,
		Instances:   1,
		Stack:       stack,
		VolumeMount: &volMount,
		Routes:      cfroutes.CFRoutes{{Port: 8080, Hostnames: []string{"dolt"}}}.RoutingInfo(),
		Ports:       []uint16{8080},

		Setup: &models.DownloadAction{
			From: "https://github.com/tedsuo/doltdb/releases/download/1.0/doltdb.zip",
			To:   ".",
		},

		Action: &models.RunAction{
			Path: "doltdb",
		},
	}

	c := newClient()

	log.Println("Creating LRP...")
	err := c.CreateDesiredLRP(lrp)
	logError(err)
}

func logError(err error) {
	if err != nil {
		log.Println("ERROR", err.Error())
	}
}

func panicError(msg string, err error) {
	if err != nil {
		log.Panicln(msg, err)
	}
}

func newClient() receptor.Client {
	return receptor.NewClient(fmt.Sprintf("http://%s:%s@receptor.%s", user, pass, target))
}
