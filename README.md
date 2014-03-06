![Inigo Montoya](http://i.imgur.com/QIVPl2n.png)

#### Setup for tests

1. Start Docker (via boot2docker)

2. Grab the fork of Drone that runs as privileged user

    ```
    go get -d github.com/vito/drone
    pushd $GOPATH/src/github.com/vito/drone
    git fetch --all
    git checkout privileged-builds
    popd
    mkdir -p $GOPATH/src/github.com/drone
    mv $GOPATH/src/github.com/vito/drone !$/drone
    ```

3. Build drone

    ```
    goto drone/drone
    make deps
    make
    mv $PWD/bin/* $GOPATH/bin
    ```

4. Make sure you have the latest inigo-ci image:

    ```
    docker pull cloudfoundry/inigo-ci
    ```

5. Run the tests

    ```
    goto inigo
    ./scripts/dev-test
    ```


#### Updating the inigo-ci image

To update the root-fs that the containers use:

    ```
    git clone https://github.com/cloudfoundry/stacks
    pushd stacks
    git checkout docker
    popd
    ```

And follow the instructions in `stacks/README.md`

These also include instructions for updating the inigo-ci docker image.  These are reproduced here:

    ```
    goto inigo
    make
    /scripts/dev-test
    docker push cloudfoundry/inigo-ci
    ```

To modify what goes into the docker image update the `Dockerfile` in the inigo repo.

#### Adding a new component to the tests

Walk through `./scripts/*` and patern-match your way to victory
