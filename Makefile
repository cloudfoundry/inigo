all: ci-image

ci-image: inigo-test-rootfs.tar
	docker build -t cloudfoundry/inigo-ci --rm .
	rm inigo-test-rootfs.tar

inigo-test-rootfs.tar: inigo-test-rootfs.cid
	docker export `cat inigo-test-rootfs.cid` > inigo-test-rootfs.tar
	docker rm `cat inigo-test-rootfs.cid`
	rm inigo-test-rootfs.cid

inigo-test-rootfs.cid:
	docker run -cidfile=inigo-test-rootfs.cid cloudfoundry/lucid64 echo
