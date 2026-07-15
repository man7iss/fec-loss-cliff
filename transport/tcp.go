package transport

import (
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"time"
)

const bulkChunk = 64 * 1024

// ListenTCP binds a TCP listener on addr. Passing ":0" or "127.0.0.1:0" yields
// an ephemeral port readable from the returned listener's Addr.
func ListenTCP(addr string) (net.Listener, error) {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("tcp listen %s: %w", addr, err)
	}
	return ln, nil
}

// ServeTCP serves bulk transfers on ln until ctx is cancelled. Each client sends
// an 8-byte payload length followed by that many bytes; the server drains them
// and replies with an 8-byte acknowledgement.
func ServeTCP(ctx context.Context, ln net.Listener) error {
	go func() {
		<-ctx.Done()
		ln.Close()
	}()
	for {
		conn, aerr := ln.Accept()
		if aerr != nil {
			if ctx.Err() != nil {
				return nil
			}
			return fmt.Errorf("tcp accept: %w", aerr)
		}
		go serveTCPConn(conn)
	}
}

func serveTCPConn(conn net.Conn) {
	var ack [8]byte

	defer conn.Close()
	n, err := io.Copy(io.Discard, conn)
	if err != nil {
		return
	}
	binary.BigEndian.PutUint64(ack[:], uint64(n))
	conn.Write(ack[:])
}

// TCPBulk streams to addr for up to maxDur (or maxBytes, if nonzero) and returns
// the delivered goodput. Transferring for a fixed duration rather than a fixed
// payload bounds runtime when the kernel congestion controller, set out of band
// via sysctl, collapses under loss. The server's acknowledged byte count divided
// by the elapsed time is the goodput.
func TCPBulk(addr string, maxDur time.Duration, maxBytes int64) (Result, error) {
	var (
		res  Result
		ack  [8]byte
		sent int64
	)

	conn, err := net.Dial("tcp", addr)
	if err != nil {
		return res, fmt.Errorf("tcp dial %s: %w", addr, err)
	}
	defer conn.Close()
	tcp := conn.(*net.TCPConn)
	buf := make([]byte, bulkChunk)
	start := time.Now()
	deadline := start.Add(maxDur)
	conn.SetWriteDeadline(deadline)
	for {
		if maxBytes > 0 && sent >= maxBytes {
			break
		}
		w, werr := conn.Write(buf)
		sent += int64(w)
		if werr != nil {
			if ne, ok := werr.(net.Error); ok && ne.Timeout() {
				break
			}
			return res, fmt.Errorf("tcp write: %w", werr)
		}
	}
	elapsed := time.Since(start)
	conn.SetWriteDeadline(time.Time{})
	tcp.CloseWrite()
	conn.SetReadDeadline(time.Now().Add(30 * time.Second))
	got := sent
	if _, err = io.ReadFull(conn, ack[:]); err == nil {
		got = int64(binary.BigEndian.Uint64(ack[:]))
	}
	res = Result{
		Arm:          ArmTCP,
		PayloadBytes: got,
		WireBytes:    got,
		Duration:     elapsed,
	}
	return res, nil
}
