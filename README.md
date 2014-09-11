#Inigo

![Inigo Montoya](http://i.imgur.com/QIVPl2n.png)

Inigo is the integration test suite for Diego, the new container management system for Cloud Foundry.  Learn more about Diego and its components at [diego-design-notes](https://github.com/cloudfoundry-incubator/diego-design-notes)

These instructions are for Mac OS X and Linux. You can run the test suite in either Concourse or Drone.

#### Setup for Concourse

1. Install and start [Concourse](https://github.com/concourse/concourse)

1. Install the Concourse CLI `go get github.com/concourse/fly`

1. Run the tests

    ```
    cd inigo
    ./scripts/dev-test
    ```

#### Setup for Drone

1. Uninstall the Concourse CLI if it is installed

1. Start Docker (via boot2docker)

1. Grab the Drone CLI

    ```
    go get github.com/drone/drone/cmd/drone
    ```

1. Make sure you have the latest inigo-ci image:

    ```
    docker pull cloudfoundry/inigo-ci
    ```

1. Run the tests

    ```
    cd inigo
    ./scripts/dev-test
    ```

#### inigo-ci docker image

Inigo runs inside a docker container, using an image called `cloudfoundry/inigo-ci`. Notably, this docker image contains *within it* a rootfs which garden will use by default. The Dockerfile and make tools can be found in [diego-dockerfiles](https://github.com/cloudfoundry-incubator/diego-dockerfiles).
