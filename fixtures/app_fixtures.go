package fixtures

import (
	"io/ioutil"

	. "github.com/onsi/gomega"
	"github.com/onsi/gomega/gexec"
	archive_helper "github.com/pivotal-golang/archiver/extractor/test_helper"
)

func GoServerApp() []archive_helper.ArchiveFile {
	serverPath, err := gexec.Build("github.com/cloudfoundry-incubator/inigo/fixtures/go-server")
	Expect(err).NotTo(HaveOccurred())

	contents, err := ioutil.ReadFile(serverPath)
	Expect(err).NotTo(HaveOccurred())
	return []archive_helper.ArchiveFile{
		{
			Name: "go-server",
			Body: string(contents),
		}, {
			Name: "staging_info.yml",
			Body: `detected_buildpack: Doesn't Matter
start_command: go-server`,
		},
	}
}

func HelloWorldIndexApp() []archive_helper.ArchiveFile {
	return []archive_helper.ArchiveFile{
		{
			Name: "app/server.sh",
			Body: `#!/bin/bash

set -e

index=$(echo $VCAP_APPLICATION | jq .instance_index)

echo "Hello World from index '${index}'"

mkfifo request

while true; do
	{
		read < request

		echo -n -e "HTTP/1.1 200 OK\r\n"
		echo -n -e "Content-Length: ${#index}\r\n\r\n"
		echo -n -e "${index}"
	} | nc -l 0.0.0.0 $PORT > request;
done
`,
		}, {
			Name: "staging_info.yml",
			Body: `detected_buildpack: Doesn't Matter
start_command: bash ./server.sh`,
		},
	}
}

func HelloWorldIndexLRP() []archive_helper.ArchiveFile {
	return []archive_helper.ArchiveFile{
		{
			Name: "server.sh",
			Body: `#!/bin/bash

set -e

index=${INSTANCE_INDEX}

echo "Hello World from index '${index}'"

server() {
	mkfifo request$1

	while true; do
		{
			read < request$1

			echo -n -e "HTTP/1.1 200 OK\r\n"
			echo -n -e "Content-Length: ${#index}\r\n\r\n"
			echo -n -e "${index}"
		} | nc -l 0.0.0.0 $1 > request$1;
	done
}

for port in $PORT; do
  server $port &
done

wait
`,
		},
	}
}

func CurlLRP() []archive_helper.ArchiveFile {
	return []archive_helper.ArchiveFile{
		{
			Name: "server.sh",
			Body: `#!/bin/bash

mkfifo request

while true; do
	{
		read < request

		echo -n -e "HTTP/1.1 200 OK\r\n"
		echo -n -e "\r\n"
		curl -s --connect-timeout 5 http://www.example.com -o /dev/null ; echo -n $?
	} | nc -l 0.0.0.0 $PORT > request;
done
`,
		},
	}
}
