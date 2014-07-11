package inigo_server

import (
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/cloudfoundry-incubator/garden/warden"
	"github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

const amazingRubyServer = `require 'webrick'
require 'json'

server = WEBrick::HTTPServer.new :Port => 8080

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
`

var container warden.Container

var ipAddress string

func Start(wardenClient warden.Client) {
	var err error

	container, err = wardenClient.Create(warden.ContainerSpec{})
	Ω(err).ShouldNot(HaveOccurred())

	info, err := container.Info()
	Ω(err).ShouldNot(HaveOccurred())

	ipAddress = info.ContainerIP

	_, err = container.Run(warden.ProcessSpec{
		Path: "ruby",
		Args: []string{"-e", amazingRubyServer},
	}, warden.ProcessIO{
		Stdout: ginkgo.GinkgoWriter,
		Stderr: ginkgo.GinkgoWriter,
	})
	Ω(err).ShouldNot(HaveOccurred())

	Eventually(func() error {
		conn, err := net.DialTimeout("tcp", ipAddress+":8080", 100*time.Millisecond)
		if err == nil {
			conn.Close()
		}

		return err
	}, 2).ShouldNot(HaveOccurred())
}

func Stop(wardenClient warden.Client) {
	wardenClient.Destroy(container.Handle())
	container = nil
	ipAddress = ""
}

func CurlArgs(guid string) []string {
	return []string{fmt.Sprintf("http://%s:8080/register?guid=%s", ipAddress, guid)}
}

func DownloadUrl(filename string) string {
	return fmt.Sprintf("http://%s:8080/file/%s", ipAddress, filename)
}

func UploadUrl(filename string) string {
	return fmt.Sprintf("http://%s:8080/upload/%s", ipAddress, filename)
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

func DownloadFile(filename string) io.Reader {
	resp, err := http.Get(DownloadUrl(filename))
	Ω(err).ShouldNot(HaveOccurred())

	return resp.Body
}

func ReportingGuids() []string {
	var responses []string
	uri := fmt.Sprintf("http://%s:8080/registrations", ipAddress)
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
