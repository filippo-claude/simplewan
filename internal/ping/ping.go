// Package ping sends ICMP echo probes bound to a specific network device.
//
// Binding each probe to its interface (SO_BINDTODEVICE) is the whole point:
// it lets us test a WAN's connectivity regardless of which WAN the routing
// table currently prefers, so a demoted-but-recovering link can still be
// observed coming back.
package ping

import (
	"errors"
	"fmt"
	"net"
	"os"
	"time"

	"golang.org/x/sys/unix"
)

// Pinger sends echo requests over one device using a raw ICMP socket.
// It requires CAP_NET_RAW (the daemon runs as root).
type Pinger struct {
	id uint16
}

// New returns a Pinger. The ICMP identifier is derived from the PID.
func New() *Pinger {
	return &Pinger{id: uint16(os.Getpid() & 0xffff)}
}

func checksum(b []byte) uint16 {
	var sum uint32
	for i := 0; i+1 < len(b); i += 2 {
		sum += uint32(b[i])<<8 | uint32(b[i+1])
	}
	if len(b)%2 == 1 {
		sum += uint32(b[len(b)-1]) << 8
	}
	for sum>>16 != 0 {
		sum = (sum & 0xffff) + (sum >> 16)
	}
	return ^uint16(sum)
}

func (p *Pinger) echoRequest(seq uint16) []byte {
	pkt := make([]byte, 16)
	pkt[0] = 8 // echo request
	pkt[1] = 0 // code
	// pkt[2:4] checksum, filled below
	pkt[4] = byte(p.id >> 8)
	pkt[5] = byte(p.id)
	pkt[6] = byte(seq >> 8)
	pkt[7] = byte(seq)
	// 8-byte payload (timestamp), contents don't matter for matching
	ts := uint64(time.Now().UnixNano())
	for i := 0; i < 8; i++ {
		pkt[8+i] = byte(ts >> (8 * uint(i)))
	}
	cs := checksum(pkt)
	pkt[2] = byte(cs >> 8)
	pkt[3] = byte(cs)
	return pkt
}

// errTimeout is returned when no matching reply arrives before the deadline.
var errTimeout = errors.New("ping timeout")

// Once sends a single echo to target via device dev and waits up to timeout
// for the matching reply, returning the round-trip time.
func (p *Pinger) Once(dev, target string, seq uint16, timeout time.Duration) (time.Duration, error) {
	ip := net.ParseIP(target)
	v4 := ip.To4()
	if v4 == nil {
		return 0, fmt.Errorf("ping: %q is not an IPv4 address", target)
	}

	fd, err := unix.Socket(unix.AF_INET, unix.SOCK_RAW, unix.IPPROTO_ICMP)
	if err != nil {
		return 0, fmt.Errorf("ping: socket: %w", err)
	}
	defer unix.Close(fd)

	if dev != "" {
		if err := unix.SetsockoptString(fd, unix.SOL_SOCKET, unix.SO_BINDTODEVICE, dev); err != nil {
			return 0, fmt.Errorf("ping: bind to %s: %w", dev, err)
		}
	}

	var dst [4]byte
	copy(dst[:], v4)
	sa := &unix.SockaddrInet4{Addr: dst}

	deadline := time.Now().Add(timeout)
	start := time.Now()
	if err := unix.Sendto(fd, p.echoRequest(seq), 0, sa); err != nil {
		return 0, fmt.Errorf("ping: sendto: %w", err)
	}

	buf := make([]byte, 1500)
	for {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return 0, errTimeout
		}
		tv := unix.NsecToTimeval(remaining.Nanoseconds())
		if err := unix.SetsockoptTimeval(fd, unix.SOL_SOCKET, unix.SO_RCVTIMEO, &tv); err != nil {
			return 0, fmt.Errorf("ping: set timeout: %w", err)
		}
		n, _, err := unix.Recvfrom(fd, buf, 0)
		if err != nil {
			if err == unix.EAGAIN || err == unix.EWOULDBLOCK || err == unix.EINTR {
				continue // covers our own deadline via remaining<=0 check
			}
			return 0, fmt.Errorf("ping: recvfrom: %w", err)
		}
		// Raw IPv4 sockets deliver the IP header; skip it.
		if n < 1 {
			continue
		}
		ihl := int(buf[0]&0x0f) * 4
		if n < ihl+8 || ihl < 20 {
			continue
		}
		icmp := buf[ihl:n]
		if icmp[0] != 0 { // not an echo reply
			continue
		}
		rid := uint16(icmp[4])<<8 | uint16(icmp[5])
		rseq := uint16(icmp[6])<<8 | uint16(icmp[7])
		if rid == p.id && rseq == seq {
			return time.Since(start), nil
		}
	}
}
