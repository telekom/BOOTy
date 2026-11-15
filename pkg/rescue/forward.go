//go:build linux

package rescue

import (
	"fmt"
	"io"
	"log/slog"
	"net"
	"strconv"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"
)

// Port forwarding exists only with RESCUE_SSH_HARDENED=false, like dropbear
// without -j -k: local (direct-tcpip, limited by permitopen=) and remote
// (tcpip-forward, listening on localhost only as dropbear without -a). A
// no-port-forwarding or restrict key option turns both off.

func (c *rescueConn) forwardingAllowed() bool { return c.legacy && !c.opts.noPortForwarding }

// directTCPIP serves a local forward (ssh -L, -W).
func (c *rescueConn) directTCPIP(nch ssh.NewChannel) {
	var req struct {
		Host     string
		Port     uint32
		OrigHost string
		OrigPort uint32
	}
	if err := ssh.Unmarshal(nch.ExtraData(), &req); err != nil || req.Port > 65535 {
		_ = nch.Reject(ssh.ConnectionFailed, "bad direct-tcpip request")
		return
	}
	if !c.forwardingAllowed() || !c.opts.allowsOpen(req.Host, int(req.Port)) {
		_ = nch.Reject(ssh.Prohibited, "port forwarding is not allowed")
		return
	}
	d := net.Dialer{Timeout: 10 * time.Second}
	tc, err := d.DialContext(c.ctx, "tcp", net.JoinHostPort(req.Host, strconv.Itoa(int(req.Port))))
	if err != nil {
		_ = nch.Reject(ssh.ConnectionFailed, err.Error())
		return
	}
	ch, reqs, err := nch.Accept()
	if err != nil {
		_ = tc.Close()
		return
	}
	go ssh.DiscardRequests(reqs)
	go pipe(ch, tc)
}

// pipe copies both ways. An EOF of one side half-closes the other (TCP
// CloseWrite, SSH EOF), so a service that answers after the end of its
// request still can; both ends close when both directions are done.
func pipe(ch ssh.Channel, tc net.Conn) {
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		_, _ = io.Copy(ch, tc)
		_ = ch.CloseWrite()
	}()
	go func() {
		defer wg.Done()
		_, _ = io.Copy(tc, ch)
		if cw, ok := tc.(interface{ CloseWrite() error }); ok {
			_ = cw.CloseWrite()
		} else {
			_ = tc.Close()
		}
	}()
	wg.Wait()
	_ = ch.Close()
	_ = tc.Close()
}

// globalRequests serves tcpip-forward and cancel-tcpip-forward; everything
// else is refused.
func (c *rescueConn) globalRequests(reqs <-chan *ssh.Request) {
	for req := range reqs {
		var reply []byte
		ok := false
		if c.forwardingAllowed() {
			switch req.Type {
			case "tcpip-forward":
				reply, ok = c.remoteForward(req.Payload)
			case "cancel-tcpip-forward":
				ok = c.cancelRemoteForward(req.Payload)
			}
		}
		if req.WantReply {
			_ = req.Reply(ok, reply)
		}
	}
}

type forwardReq struct {
	Addr string
	Port uint32
}

func (c *rescueConn) remoteForward(payload []byte) ([]byte, bool) {
	var req forwardReq
	if ssh.Unmarshal(payload, &req) != nil || req.Port > 65535 {
		return nil, false
	}
	var lc net.ListenConfig
	ln, err := lc.Listen(c.ctx, "tcp", net.JoinHostPort("localhost", strconv.Itoa(int(req.Port))))
	if err != nil {
		slog.Warn("rescue SSH remote forward failed", "port", req.Port, "error", err)
		return nil, false
	}
	port := uint32(ln.Addr().(*net.TCPAddr).Port) //nolint:gosec,forcetypeassert // a TCP port
	c.fwdMu.Lock()
	if c.fwds == nil {
		c.fwds = map[string]net.Listener{}
	}
	c.fwds[fmt.Sprintf("%s:%d", req.Addr, port)] = ln
	c.fwdMu.Unlock()
	go c.acceptForward(ln, req.Addr, port)
	return ssh.Marshal(struct{ Port uint32 }{port}), true
}

func (c *rescueConn) acceptForward(ln net.Listener, addr string, port uint32) {
	for {
		tc, err := ln.Accept()
		if err != nil {
			return
		}
		go func() {
			oh, op, _ := net.SplitHostPort(tc.RemoteAddr().String())
			opn, _ := strconv.ParseUint(op, 10, 16)
			ch, reqs, err := c.conn.OpenChannel("forwarded-tcpip", ssh.Marshal(struct {
				Addr     string
				Port     uint32
				OrigAddr string
				OrigPort uint32
			}{addr, port, oh, uint32(opn)}))
			if err != nil {
				_ = tc.Close()
				return
			}
			go ssh.DiscardRequests(reqs)
			pipe(ch, tc)
		}()
	}
}

func (c *rescueConn) cancelRemoteForward(payload []byte) bool {
	var req forwardReq
	if ssh.Unmarshal(payload, &req) != nil {
		return false
	}
	key := fmt.Sprintf("%s:%d", req.Addr, req.Port)
	c.fwdMu.Lock()
	defer c.fwdMu.Unlock()
	ln, ok := c.fwds[key]
	if ok {
		_ = ln.Close()
		delete(c.fwds, key)
	}
	return ok
}

// closeForwards stops the remote forwards of the connection.
func (c *rescueConn) closeForwards() {
	c.fwdMu.Lock()
	defer c.fwdMu.Unlock()
	for _, ln := range c.fwds {
		_ = ln.Close()
	}
	c.fwds = nil
}
