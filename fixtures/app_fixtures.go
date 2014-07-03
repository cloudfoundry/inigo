package fixtures

import archive_helper "github.com/pivotal-golang/archiver/extractor/test_helper"

func HelloWorldIndexApp() []archive_helper.ArchiveFile {
	return []archive_helper.ArchiveFile{
		{
			Name: "app/run",
			Body: `#!/bin/bash
          ruby <<END_MAGIC_SERVER
require 'webrick'
require 'json'

STDOUT.sync = true

server = WEBrick::HTTPServer.new :BindAddress => "0.0.0.0", :Port => ENV['PORT']

index = JSON.parse(ENV["VCAP_APPLICATION"])["instance_index"]
puts "Hello World from index '#{index}'"

server.mount_proc '/' do |req, res|
  res.body = index.to_s
  res.status = 200
end

trap('INT') {
  server.shutdown
}

server.start
END_MAGIC_SERVER
          `,
		},
	}
}
