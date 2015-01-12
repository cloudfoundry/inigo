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
		},
	}
}
