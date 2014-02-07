package inigolistener

import (
	"encoding/json"
	"fmt"
	. "github.com/onsi/gomega"
	"github.com/vito/gordon"
	"io/ioutil"
	"net/http"
)

const amazingRubyServer = `ruby <<END_MAGIC_SERVER
require 'webrick'
require 'json'

server = WEBrick::HTTPServer.new :Port => ENV['PORT']

registered = []

server.mount_proc '/register' do |req, res|
  registered << req.query['guid']
  res.status = 200
end

server.mount_proc '/registrations' do |req, res|
    res.body = JSON.generate(registered)
end

server.mount_proc '/reset' do |req, res|
    registered = []
    res.status = 200
end

trap('INT') {
    server.shutdown
}
 
server.start
END_MAGIC_SERVER
`

var handle string
var hostPort uint32
var ipAddress string

func Start(wardenClient gordon.Client) {
	createResponse, err := wardenClient.Create()
	if err != nil {
		panic(err)
	}

	handle = createResponse.GetHandle()
	println("listener " + handle)

	netResponse, err := wardenClient.NetIn(handle)
	if err != nil {
		panic(err)
	}

	containerPort := netResponse.GetContainerPort()
	hostPort = netResponse.GetHostPort()

	infoResponse, err := wardenClient.Info(handle)
	if err != nil {
		panic(err)
	}
	ipAddress = infoResponse.GetContainerIp()

	_, err = wardenClient.Spawn(handle, fmt.Sprintf("PORT=%d %s", containerPort, amazingRubyServer), true)
	if err != nil {
		panic(err)
	}

	Eventually(func() error {
		_, err := http.Get(fmt.Sprintf("http://%s:%d/registrations", ipAddress, hostPort))
		return err
	}).ShouldNot(HaveOccurred())
}

func Stop(wardenClient gordon.Client) {
	wardenClient.Destroy(handle)
}

func CurlCommand(guid string) string {
	curlCommand := fmt.Sprintf("curl http://%s:%d/register?guid=%s", ipAddress, hostPort, guid)
	return curlCommand
}

func ReportingGuids() []string {
	var responses []string
	uri := fmt.Sprintf("http://%s:%d/registrations", ipAddress, hostPort)
	response, err := http.Get(uri)
	if err != nil {
		panic("Problem getting reporting guids from the tiny server")
	}
	defer response.Body.Close()
	body, err := ioutil.ReadAll(response.Body)
	err = json.Unmarshal(body, &responses)
	if err != nil {
		panic("Could not unmarshal responses from the tiny server")
	}

	return responses
}
