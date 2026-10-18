package network

import (
	"context"
	"errors"
	"net"
	"strings"
	"sync"
	"time"
)

// dnsFanout makes the DNS lookups of the process send every UDP query to
// all DNS servers at once and take the first answer, like
// systemd-resolved in the legacy ramdisk queries the servers of every link
// (the lease DNS servers of each NIC, plus the global ones). The Go
// resolver alone asks the first resolv.conf server and the next only when
// it fails. DHCP mode sets the servers whenever it writes resolv.conf.
var dnsFanout fanout

// dnsPort is the server port; tests change it.
var dnsPort = "53"

type fanout struct {
	mu      sync.Mutex
	servers []string
	once    sync.Once
}

// set replaces the servers and puts the fan-out resolver in place.
func (f *fanout) set(servers []string) {
	f.mu.Lock()
	f.servers = servers
	f.mu.Unlock()
	f.once.Do(func() {
		net.DefaultResolver = &net.Resolver{PreferGo: true, Dial: f.dial}
	})
}

func (f *fanout) list() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.servers
}

// dial gives the Go resolver one connection for a query to address. Over
// UDP with more than one server it is a fan-out to all of them; TCP (a
// truncated answer) goes to address only.
func (f *fanout) dial(ctx context.Context, network, address string) (net.Conn, error) {
	servers := f.list()
	var d net.Dialer
	if len(servers) < 2 || !strings.HasPrefix(network, "udp") {
		return d.DialContext(ctx, network, address) //nolint:wrapcheck // the resolver reports it
	}
	c := &fanoutConn{replies: make(chan []byte, 4*len(servers)), errs: make(chan error, len(servers))}
	for _, s := range servers {
		conn, err := d.DialContext(ctx, "udp", net.JoinHostPort(s, dnsPort))
		if err == nil {
			c.conns = append(c.conns, conn)
		}
	}
	if len(c.conns) == 0 {
		return d.DialContext(ctx, network, address) //nolint:wrapcheck // the resolver reports it
	}
	return c, nil
}

// fanoutConn writes a query to every server and reads the first answer. It
// is a net.PacketConn, so the resolver uses UDP framing.
type fanoutConn struct {
	conns   []net.Conn
	start   sync.Once
	replies chan []byte
	errs    chan error
}

func (c *fanoutConn) Write(b []byte) (int, error) {
	errs := make([]error, 0, len(c.conns))
	for _, conn := range c.conns {
		if _, err := conn.Write(b); err != nil {
			errs = append(errs, err)
		}
	}
	if len(errs) == len(c.conns) {
		return 0, errors.Join(errs...)
	}
	return len(b), nil
}

func (c *fanoutConn) Read(b []byte) (int, error) {
	c.start.Do(func() {
		for _, conn := range c.conns {
			go func() {
				buf := make([]byte, 65535)
				for {
					n, err := conn.Read(buf)
					if err != nil {
						c.errs <- err
						return
					}
					select {
					case c.replies <- append([]byte(nil), buf[:n]...):
					default: // nobody reads any more answers
					}
				}
			}()
		}
	})
	var failed []error
	for {
		select {
		case r := <-c.replies:
			return copy(b, r), nil
		case err := <-c.errs:
			if failed = append(failed, err); len(failed) == len(c.conns) {
				return 0, failed[0] // all timed out or failed
			}
		}
	}
}

func (c *fanoutConn) ReadFrom(b []byte) (int, net.Addr, error) {
	n, err := c.Read(b)
	return n, c.RemoteAddr(), err
}

func (c *fanoutConn) WriteTo(b []byte, _ net.Addr) (int, error) { return c.Write(b) }

func (c *fanoutConn) Close() error {
	errs := make([]error, 0, len(c.conns))
	for _, conn := range c.conns {
		errs = append(errs, conn.Close())
	}
	return errors.Join(errs...)
}

func (c *fanoutConn) LocalAddr() net.Addr  { return c.conns[0].LocalAddr() }
func (c *fanoutConn) RemoteAddr() net.Addr { return c.conns[0].RemoteAddr() }

func (c *fanoutConn) SetDeadline(t time.Time) error {
	errs := make([]error, 0, len(c.conns))
	for _, conn := range c.conns {
		errs = append(errs, conn.SetDeadline(t))
	}
	return errors.Join(errs...)
}

func (c *fanoutConn) SetReadDeadline(t time.Time) error {
	errs := make([]error, 0, len(c.conns))
	for _, conn := range c.conns {
		errs = append(errs, conn.SetReadDeadline(t))
	}
	return errors.Join(errs...)
}

func (c *fanoutConn) SetWriteDeadline(t time.Time) error {
	errs := make([]error, 0, len(c.conns))
	for _, conn := range c.conns {
		errs = append(errs, conn.SetWriteDeadline(t))
	}
	return errors.Join(errs...)
}
