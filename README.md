#Inigo

![Inigo Montoya](http://i.imgur.com/QIVPl2n.png)

####Learn more about Diego and its components at [diego-design-notes](https://github.com/cloudfoundry-incubator/diego-design-notes)

#### Setup for tests

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

Walk through `./scripts/*` and pattern-match your way to victory
