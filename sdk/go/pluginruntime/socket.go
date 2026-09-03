package pluginruntime

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"time"

	"github.com/DeliciousBuding/cloud-path/sdk/go/transport"
)

// writeFrame writes payload as a 4-byte big-endian length followed by the
// payload bytes.
func writeFrame(w io.Writer, payload []byte) error {
	var hdr [4]byte
	binary.BigEndian.PutUint32(hdr[:], uint32(len(payload)))
	if _, err := w.Write(hdr[:]); err != nil {
		return err
	}
	if _, err := w.Write(payload); err != nil {
		return err
	}
	return nil
}

// readFrame reads one length-prefixed frame, rejecting empty frames and frames
// larger than max.
func readFrame(r io.Reader, max int) ([]byte, error) {
	var hdr [4]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return nil, err
	}
	n := binary.BigEndian.Uint32(hdr[:])
	if n == 0 {
		return nil, errors.New("pluginruntime: empty frame")
	}
	if int64(n) > int64(max) {
		return nil, ErrFrameTooLarge
	}
	buf := make([]byte, int(n))
	if _, err := io.ReadFull(r, buf); err != nil {
		return nil, err
	}
	return buf, nil
}

// socketConn adapts one authenticated net.Conn to transport.Transport using
// length-prefixed JSON transport.Message frames. Send is safe for concurrent
// use; Recv is expected to be called from a single reader goroutine.
type socketConn struct {
	conn net.Conn
	cfg  Config
	wmu  sync.Mutex
}

func newSocketConn(conn net.Conn, cfg Config) *socketConn {
	return &socketConn{conn: conn, cfg: cfg}
}

func (c *socketConn) Send(ctx context.Context, m transport.Message) error {
	body, err := json.Marshal(m)
	if err != nil {
		return fmt.Errorf("pluginruntime: marshal frame: %w", err)
	}
	if len(body) > c.cfg.MaxFrameSize {
		return ErrFrameTooLarge
	}
	c.wmu.Lock()
	defer c.wmu.Unlock()
	if err := c.conn.SetWriteDeadline(opDeadline(ctx, c.cfg.WriteTimeout)); err != nil {
		return err
	}
	return writeFrame(c.conn, body)
}

func (c *socketConn) Recv(ctx context.Context) (transport.Message, error) {
	if err := c.conn.SetReadDeadline(opDeadline(ctx, c.cfg.ReadTimeout)); err != nil {
		return transport.Message{}, err
	}
	body, err := readFrame(c.conn, c.cfg.MaxFrameSize)
	if err != nil {
		return transport.Message{}, err
	}
	var m transport.Message
	if err := json.Unmarshal(body, &m); err != nil {
		return transport.Message{}, fmt.Errorf("pluginruntime: unmarshal frame: %w", err)
	}
	return m, nil
}

func (c *socketConn) Close() error {
	return c.conn.Close()
}

// opDeadline returns the earlier of now+timeout and the context deadline.
func opDeadline(ctx context.Context, timeout time.Duration) time.Time {
	d := time.Now().Add(timeout)
	if ctx != nil {
		if dd, ok := ctx.Deadline(); ok && dd.Before(d) {
			d = dd
		}
	}
	return d
}
