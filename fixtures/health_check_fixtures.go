package fixtures

import archive_helper "github.com/pivotal-golang/archiver/extractor/test_helper"

func SuccessfulHealthCheck() []archive_helper.ArchiveFile {
	return []archive_helper.ArchiveFile{
		{
			Name: "diego-health-check",
			Body: `#!/bin/bash
        exit 0
        `,
		},
	}
}
