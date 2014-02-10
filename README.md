![Inigo Montoya](http://i.imgur.com/QIVPl2n.png)

#### Setup for tests

1. Start Docker listening on port 4243

    ```
    go get github.com/coreos/docker
    goto docker
    export FORWARD_PORTS=4243
    vagrant up
    vagrant reload
    vagrant ssh -c 'sudo service docker stop; sudo /usr/bin/docker -d -H tcp://0.0.0.0:4243'
    ```

2. Grab the fork of Drone that runs as privileged user

    ```
    go get -d github.com/vito/drone
    mkdir -p $GOPATH/src/github.com/drone
    mv $GOPATH/src/github.com/vito/drone !$/drone
    ```

3. Build drone

    ```
    goto drone/drone
    make deps
    make
    export PATH=$PWD/bin:$PATH
    ```

4. Run the tests

    ```
    goto inigo
    ./scripts/dev-test
    ```