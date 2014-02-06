![Inigo Montoya](http://i.imgur.com/QIVPl2n.png)

#### Setup for tests
1. Start Garden - see README.md
1. set WARDEN_NETWORK & WARDEN_ADDR
```
VirtualBox
export WARDEN_NETWORK=tcp
export WARDEN_ADDR=192.168.50.1:7031
```
1. Install gnatsd  
  go install github.com/apcera/gnatsd
