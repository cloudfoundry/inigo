all: ci-image

ci-image:
	docker save cloudfoundry/lucid64 > inigo-test-rootfs.tar
	docker build -t cloudfoundry/inigo-ci --rm .
	rm inigo-test-rootfs.tar
