# Inigo

Inigo is the integration test suite for Diego, the new container management
system for Cloud Foundry. Learn more about Diego and its components at
[diego-design-notes](https://github.com/cloudfoundry-incubator/diego-design-notes)

These instructions are for Mac OS X and Linux.


#### Running Tests

Inigo runs against many components, all of which live in the [Diego BOSH
Release](https://github.com/cloudfoundry-incubator/diego-release).

To run Inigo, follow the instructions in Diego Release's
[README](https://github.com/cloudfoundry-incubator/diego-release/blob/develop/README.md#running-integration-tests) section `Running Integration Tests`.


#### The `inigo-ci` docker image

Inigo runs inside a container, using the `cloudfoundry/inigo-ci` Docker image.
This docker image contains *within it* a rootfs which Garden will use by
default.

To (re-)build this image, see
[diego-dockerfiles](https://github.com/cloudfoundry-incubator/diego-dockerfiles).
