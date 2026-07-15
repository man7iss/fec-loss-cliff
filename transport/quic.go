package transport

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/binary"
	"fmt"
	"io"
	"math/big"
	"net"
	"time"

	"github.com/quic-go/quic-go"
)

const alpn = "feccliff-h1"

// Transfer modes carried in the one-byte control header a client sends before
// any data.
const (
	modeBulk     = 1
	modeDeadline = 2
)

const headerLen = 1 + 8 + 8 + 8

// serverTLS returns a TLS config with a freshly generated self-signed
// certificate. The harness measures transport behaviour, not the PKI, so the
// matching client trusts the certificate without verification.
func serverTLS() (*tls.Config, error) {
	var tmpl x509.Certificate

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("generate key: %w", err)
	}
	tmpl = x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "feccliff"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
	}
	der, err := x509.CreateCertificate(rand.Reader, &tmpl, &tmpl, &key.PublicKey, key)
	if err != nil {
		return nil, fmt.Errorf("create cert: %w", err)
	}
	cert := tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}
	return &tls.Config{Certificates: []tls.Certificate{cert}, NextProtos: []string{alpn}}, nil
}

func clientTLS() *tls.Config {
	return &tls.Config{InsecureSkipVerify: true, NextProtos: []string{alpn}}
}

func quicConfig() *quic.Config {
	return &quic.Config{EnableDatagrams: true, MaxIdleTimeout: 30 * time.Second}
}

func putHeader(mode byte, p1, p2, p3 uint64) []byte {
	buf := make([]byte, headerLen)
	buf[0] = mode
	binary.BigEndian.PutUint64(buf[1:], p1)
	binary.BigEndian.PutUint64(buf[9:], p2)
	binary.BigEndian.PutUint64(buf[17:], p3)
	return buf
}

func readHeader(r io.Reader) (byte, uint64, uint64, uint64, error) {
	var buf [headerLen]byte

	if _, err := io.ReadFull(r, buf[:]); err != nil {
		return 0, 0, 0, 0, err
	}
	return buf[0], binary.BigEndian.Uint64(buf[1:]), binary.BigEndian.Uint64(buf[9:]), binary.BigEndian.Uint64(buf[17:]), nil
}

// ListenQUIC binds a QUIC listener on addr with datagram support enabled. The
// same listener type backs both the plain-QUIC and the FEC serve loops.
func ListenQUIC(addr string) (*quic.Listener, error) {
	tlsConf, err := serverTLS()
	if err != nil {
		return nil, err
	}
	ln, err := quic.ListenAddr(addr, tlsConf, quicConfig())
	if err != nil {
		return nil, fmt.Errorf("quic listen %s: %w", addr, err)
	}
	return ln, nil
}

// ServeQUIC serves bulk and deadline transfers over a single bidirectional
// stream per connection until ctx is cancelled.
func ServeQUIC(ctx context.Context, ln *quic.Listener) error {
	go func() {
		<-ctx.Done()
		ln.Close()
	}()
	for {
		conn, aerr := ln.Accept(ctx)
		if aerr != nil {
			if ctx.Err() != nil {
				return nil
			}
			return fmt.Errorf("quic accept: %w", aerr)
		}
		go serveQUICConn(ctx, conn)
	}
}

func serveQUICConn(ctx context.Context, conn *quic.Conn) {
	stream, err := conn.AcceptStream(ctx)
	if err != nil {
		return
	}
	mode, p1, p2, p3, err := readHeader(stream)
	if err != nil {
		return
	}
	switch mode {
	case modeBulk:
		serveQUICBulk(stream)
	case modeDeadline:
		serveStreamDeadline(stream, int(p1), int(p2), time.Duration(p3))
	}
	// The client reads the acknowledgement and then closes the connection; a
	// server-side close here would race the ack off the wire.
}

func serveQUICBulk(stream *quic.Stream) {
	var ack [8]byte

	n, err := io.Copy(io.Discard, stream)
	if err != nil {
		return
	}
	binary.BigEndian.PutUint64(ack[:], uint64(n))
	stream.Write(ack[:])
	stream.Close()
}

// QUICBulk streams over one QUIC stream for up to maxDur (or maxBytes) and
// returns the delivered goodput. This is the plain-QUIC baseline: no FEC,
// quic-go's own congestion controller, one ordered stream.
func QUICBulk(addr string, maxDur time.Duration, maxBytes int64) (Result, error) {
	var (
		res  Result
		ack  [8]byte
		sent int64
	)

	ctx := context.Background()
	conn, err := quic.DialAddr(ctx, addr, clientTLS(), quicConfig())
	if err != nil {
		return res, fmt.Errorf("quic dial %s: %w", addr, err)
	}
	defer conn.CloseWithError(0, "")
	stream, err := conn.OpenStreamSync(ctx)
	if err != nil {
		return res, fmt.Errorf("quic open stream: %w", err)
	}
	if _, err = stream.Write(putHeader(modeBulk, 0, 0, 0)); err != nil {
		return res, fmt.Errorf("quic write header: %w", err)
	}
	buf := make([]byte, bulkChunk)
	start := time.Now()
	deadline := start.Add(maxDur)
	stream.SetWriteDeadline(deadline)
	for {
		if maxBytes > 0 && sent >= maxBytes {
			break
		}
		w, werr := stream.Write(buf)
		sent += int64(w)
		if werr != nil {
			if ne, ok := werr.(net.Error); ok && ne.Timeout() {
				break
			}
			return res, fmt.Errorf("quic write: %w", werr)
		}
	}
	elapsed := time.Since(start)
	stream.SetWriteDeadline(time.Time{})
	stream.Close()
	stream.SetReadDeadline(time.Now().Add(30 * time.Second))
	got := sent
	if _, err = io.ReadFull(stream, ack[:]); err == nil {
		got = int64(binary.BigEndian.Uint64(ack[:]))
	}
	res = Result{
		Arm:          ArmQUIC,
		PayloadBytes: got,
		WireBytes:    got,
		Duration:     elapsed,
	}
	return res, nil
}
