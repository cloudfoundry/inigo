package inigolistener

import (
	"encoding/json"
	"fmt"
	"github.com/onsi/ginkgo/config"
	. "github.com/onsi/gomega"
	"github.com/vito/gordon"
	"io/ioutil"
	"net/http"
	"os"
	"strings"
)

const amazingRubyServer = `ruby <<END_MAGIC_SERVER
require 'webrick'
require 'json'

server = WEBrick::HTTPServer.new :Port => ENV['PORT']

registered = []
files = {}

server.mount_proc '/register' do |req, res|
  registered << req.query['guid']
  res.status = 200
end

server.mount_proc '/registrations' do |req, res|
    res.body = JSON.generate(registered)
end

server.mount_proc '/upload' do |req, res|
    filename = req.request_uri.to_s.split('/').last
    STDERR.write "UPLOADING A FILE\n"
    STDERR.write "FILENAME: #{filename}\n"
    STDERR.write "BODY: #{req.body.inspect}\n"
    files[filename] = req.body
    res.status = 200
end

server.mount_proc '/file' do |req, res|
    filename = req.request_uri.to_s.split('/').last
    STDERR.write "DOWNLOADING A FILE\n"
    STDERR.write "FILENAME: #{filename}\n"
    STDERR.write "BODY: #{files[filename].inspect}\n"
    if files[filename]
        res.body = files[filename]
        res.status = 200
    else
        res.status = 404
    end
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

	_, stream, err := wardenClient.Run(handle, fmt.Sprintf("PORT=%d %s", containerPort, amazingRubyServer))
	if err != nil {
		panic(err)
	}

	if config.DefaultReporterConfig.Verbose {
		go func() {
			for response := range stream {
				fmt.Printf("[InigoListener]: %s", response.GetData())
			}
		}()
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

func DownloadUrl(filename string) string {
	return fmt.Sprintf("http://%s:%d/file/%s", ipAddress, hostPort, filename)
}

func UploadUrl(filename string) string {
	return fmt.Sprintf("http://%s:%d/upload/%s", ipAddress, hostPort, filename)
}

func DownloadFileString(filename string) string {
	resp, err := http.Get(DownloadUrl(filename))
	Ω(err).ShouldNot(HaveOccurred())

	body, err := ioutil.ReadAll(resp.Body)
	Ω(err).ShouldNot(HaveOccurred())

	resp.Body.Close()

	return string(body)
}

func UploadFileString(filename string, body string) {
	_, err := http.Post(UploadUrl(filename), "application/octet-stream", strings.NewReader(body))
	Ω(err).ShouldNot(HaveOccurred())
}

func UploadFile(filename string, filepath string) {
	file, err := os.Open(filepath)
	Ω(err).ShouldNot(HaveOccurred())
	_, err = http.Post(UploadUrl(filename), "application/octet-stream", file)
	Ω(err).ShouldNot(HaveOccurred())
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
