package goproxy

import (
	"bytes"
	"net"
)

// wireTapConn wraps a MITM client connection (post-TLS-decrypt) and records the
// raw bytes read from and written to it, so handleHttps can hand each request's
// and response's on-wire bytes to ProxyHttpServer.WireTap. It is only installed
// when WireTap is non-nil, so it adds no overhead to the default path.
type wireTapConn struct {
	net.Conn
	readBuf  bytes.Buffer
	writeBuf bytes.Buffer
}

func (c *wireTapConn) Read(p []byte) (int, error) {
	n, err := c.Conn.Read(p)
	if n > 0 {
		c.readBuf.Write(p[:n])
	}
	return n, err
}

func (c *wireTapConn) Write(p []byte) (int, error) {
	n, err := c.Conn.Write(p)
	if n > 0 {
		c.writeBuf.Write(p[:n])
	}
	return n, err
}

// takeRequest returns a copy of the raw bytes read for the just-completed
// request and removes them from the buffer, leaving `buffered` bytes of
// read-ahead (which belong to the next request on a keep-alive connection) in
// place. `buffered` is the reader's Buffered() count at the moment all of the
// current request has been consumed. Returns nil when nothing was consumed.
func (c *wireTapConn) takeRequest(buffered int) []byte {
	consumed := c.readBuf.Len() - buffered
	if consumed <= 0 {
		return nil
	}
	out := make([]byte, consumed)
	copy(out, c.readBuf.Next(consumed))
	return out
}

// takeResponse returns a copy of the raw bytes written since the last call and
// clears the write buffer. Returns nil when nothing was written.
func (c *wireTapConn) takeResponse() []byte {
	if c.writeBuf.Len() == 0 {
		return nil
	}
	out := make([]byte, c.writeBuf.Len())
	copy(out, c.writeBuf.Bytes())
	c.writeBuf.Reset()
	return out
}
