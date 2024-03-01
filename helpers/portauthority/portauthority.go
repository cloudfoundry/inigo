package portauthority

import (
	"errors"
	"fmt"
)

type PortAllocator interface {
	ClaimPorts(int) (uint16, error)
	PortIsAvailable(int) error
}

type portAllocator struct {
	host       string
	nextPort   uint16
	endingPort uint16
}

// New creates a new port allocator
// startingPort indicates the first port that will be assigned by the ClaimPorts() function.
// endingPort indicates the maximum port number that this allocator may assign.
//
// returns a non-nil error if the starting or ending port are outside the IANA range of 0-65535.
func New(host string, startingPort, endingPort int) (PortAllocator, error) {
	if startingPort < 0 {
		return nil, errors.New("Invalid starting port requested. Ports can only be numbers between 0-65535")
	}
	if endingPort > 65535 {
		return nil, errors.New("Invalid ending port requested. Ports can only be numbers between 0-65535")
	}
	return &portAllocator{
		host:       host,
		nextPort:   uint16(startingPort),
		endingPort: uint16(endingPort),
	}, nil
}

// ClaimPorts returns a new uint16 port to be used for testing processes.
//
// No guarantees are made that something is not already listening on that port.
// If running multiple processes, you should initialize the portAllocator with different ranges.
// If ports are also allocated by another method, the portAllocator should be
// provided with a range that skips those other ports.
//
// numPorts indicates the number of ports that will be claimed. The first claimed
// port is returned, and the next numPorts-1 ports sequentially after that are yours
// to use.
//
// returns a non-nil error if there are not enough ports in the range compared to
// the number requested.
func (p *portAllocator) ClaimPorts(numPorts int) (uint16, error) {
	port := p.nextPort
	if p.endingPort < port+uint16(numPorts-1) {
		return 0, errors.New("insufficient ports available")
	}

	err := p.PortIsAvailable(int(port))
	if err != nil {
		return 0, errors.New(fmt.Sprintf("port %d is not available: %s", port, err))
	}

	p.nextPort = p.nextPort + uint16(numPorts)
	return uint16(port), nil
}

func (p *portAllocator) PortIsAvailable(port int) error {
	return nil
	// listener, err := net.Listen("tcp", fmt.Sprintf("%s:%d", p.host, port))
	// if err != nil {
	// 	return errors.New(fmt.Sprintf("Port is already in use: %s", err))
	// }
	// err = listener.Close()
	// if err != nil {
	// 	return errors.New(fmt.Sprintf("Could not stop listening on port: %s", err))
	// }
	// return nil
}
