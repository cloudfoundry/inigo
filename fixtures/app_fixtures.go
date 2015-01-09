package fixtures

import archive_helper "github.com/pivotal-golang/archiver/extractor/test_helper"

func HelloWorldIndexApp() []archive_helper.ArchiveFile {
	return []archive_helper.ArchiveFile{
		{
			Name: "app/server.sh",
			Body: `#!/bin/bash

set -e

index=$(echo $VCAP_APPLICATION | jq .instance_index)

echo "Hello World from index '${index}'"

while true; do {
  # note that the following must be one single write
  echo -n -e "HTTP/1.1 200 OK\r\nContent-Length: ${#index}\r\n\r\n${index}"
} | nc -l 0.0.0.0 $PORT; done
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

while true; do {
  # note that the following must be one single write
  echo -n -e "HTTP/1.1 200 OK\r\nContent-Length: ${#index}\r\n\r\n${index}"
} | nc -l 0.0.0.0 $PORT; done
`,
		},
	}
}
