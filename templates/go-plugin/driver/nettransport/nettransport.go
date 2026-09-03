// Package nettransport provides a reference transport.Transport over a
// net.Conn. It lets the template plugin and a host exchange CloudPath RPC
// frames over loopback TCP (or any net.Conn), which is the development
// transport named in the plugin handshake. It uses stdlib only.
//
// Frame layout: a 4-byte big-endian length followed by the JSON form of
// transport.Message. This is the seam a real A4 Plugin Host must implement
// symmetrically on its side; the in-memory transport.Pipe is the canonical
// test transport and this adapter is for running the binary against a host.
package nettransport

import (
	"bufio"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"net"
	"sync"

	"github.com/DeliciousBuding/cloud-path/sdk/go/cloudpath/v1/status"
	"github.com/DeliciousBuding/cloud-path/sdk/go/transport"
)

const maxFrame = 64 << 20 // 64 MiB guard against a corrupt length prefix

type netTransport struct {
	conn net.Conn
	r    *bufio.Reader
	wMu  sync.Mutex
	once sync.Once
}

// New wraps conn as a transport.Transport.
func New(conn net.Conn) transport.Transport {
	return &netTransport{conn: conn, r: bufio.NewReader(conn)}
}

func (t *netTransport) Send(ctx context.Context, m transport.Message) error {
	body, err := json.Marshal(m)
	if err != nil {
		return err
	}
	if len(body) > maxFrame {
		return status.Errorf(status.CodeResourceExhausted, "frame too large: %d bytes", len(body))
	}
	frame := make([]byte, 4+len(body))
	binary.BigEndian.PutUint32(frame[:4], uint32(len(body)))
	copy(frame[4:], body)

	t.wMu.Lock()
	defer t.wMu.Unlock()
	if err := writeFull(ctx, t.conn, frame); err != nil {
		return err
	}
	return nil
}

func (t *netTransport) Recv(ctx context.Context) (transport.Message, error) {
	var hdr [4]byte
	if err := readFull(ctx, t.r, hdr[:]); err != nil {
		return transport.Message{}, err
	}
	n := binary.BigEndian.Uint32(hdr[:])
	if n == 0 || n > maxFrame {
		return transport.Message{}, status.Errorf(status.CodeDataLoss, "bad frame length %d", n)
	}
	body := make([]byte, n)
	if err := readFull(ctx, t.r, body); err != nil {
		return transport.Message{}, err
	}
	var m transport.Message
	if err := json.Unmarshal(body, &m); err != nil {
		return transport.Message{}, status.Errorf(status.CodeDataLoss, "decode frame: %v", err)
	}
	return m, nil
}

func (t *netTransport) Close() error {
	var err error
	t.once.Do(func() { err = t.conn.Close() })
	return err
}

// writeFull writes all of b, honoring ctx cancellation.
func writeFull(ctx context.Context, w io.Writer, b []byte) error {
	for len(b) > 0 {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		n, err := w.Write(b)
		if err != nil {
			if errors.Is(err, io.ErrClosedPipe) || errors.Is(err, net.ErrClosed) {
				return transport.ErrClosed
			}
			return err
		}
		b = b[n:]
	}
	return nil
}

// readFull reads exactly len(b), honoring ctx cancellation.
func readFull(ctx context.Context, r io.Reader, b []byte) error {
	for n := 0; n < len(b); {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		m, err := r.Read(b[n:])
		if m > 0 {
			n += m
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				return io.EOF
			}
			if errors.Is(err, io.ErrClosedPipe) || errors.Is(err, net.ErrClosed) {
				return transport.ErrClosed
			}
			return err
		}
		if m == 0 {
			return io.ErrUnexpectedEOF
		}
	}
	return nil
}
