package transport

import (
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"time"

	"github.com/quic-go/quic-go"
)

// Each application data unit begins with an 8-byte sequence number and an 8-byte
// client send timestamp (wall-clock nanoseconds). The client and server run on
// the same host in this harness, so the wall clock is a shared reference.
const aduHeaderLen = 16

func putADU(buf []byte, seq uint64, sendNanos int64) {
	binary.BigEndian.PutUint64(buf[0:], seq)
	binary.BigEndian.PutUint64(buf[8:], uint64(sendNanos))
}

// serveStreamDeadline reads numADU in-order application data units from a
// reliable QUIC stream, counts how many arrive within deadline of their send
// time, and replies with the on-time and total counts. Because the stream is
// reliable, a lost packet delays every unit behind it: this arm exhibits the
// head-of-line blocking that the datagram arm avoids.
func serveStreamDeadline(stream *quic.Stream, numADU, aduBytes int, deadline time.Duration) {
	var (
		buf    = make([]byte, aduBytes)
		ack    [16]byte
		onTime int
	)

	for i := 0; i < numADU; i++ {
		if _, err := io.ReadFull(stream, buf); err != nil {
			break
		}
		arr := time.Now().UnixNano()
		sent := int64(binary.BigEndian.Uint64(buf[8:16]))
		if time.Duration(arr-sent) <= deadline {
			onTime++
		}
	}
	binary.BigEndian.PutUint64(ack[0:], uint64(onTime))
	binary.BigEndian.PutUint64(ack[8:], uint64(numADU))
	stream.Write(ack[:])
	stream.Close()
}

// QUICDeadline sends numADU application data units of aduBytes each, one every
// period, over a single reliable QUIC stream, and reports how many the server
// received within deadline. It is the in-order baseline for the deadline
// workload.
func QUICDeadline(addr string, numADU, aduBytes int, period, deadline time.Duration) (Result, error) {
	var (
		res Result
		ack [16]byte
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
	if _, err = stream.Write(putHeader(modeDeadline, uint64(numADU), uint64(aduBytes), uint64(deadline))); err != nil {
		return res, fmt.Errorf("quic write header: %w", err)
	}
	buf := make([]byte, aduBytes)
	start := time.Now()
	for i := 0; i < numADU; i++ {
		deadlineFor := start.Add(time.Duration(i) * period)
		if d := time.Until(deadlineFor); d > 0 {
			time.Sleep(d)
		}
		putADU(buf, uint64(i), time.Now().UnixNano())
		if _, err = stream.Write(buf); err != nil {
			return res, fmt.Errorf("quic write adu: %w", err)
		}
	}
	if _, err = io.ReadFull(stream, ack[:]); err != nil {
		return res, fmt.Errorf("quic read ack: %w", err)
	}
	res = Result{
		Arm:          ArmQUIC,
		PayloadBytes: int64(numADU) * int64(aduBytes),
		WireBytes:    int64(numADU) * int64(aduBytes),
		Duration:     time.Since(start),
		OnTime:       int(binary.BigEndian.Uint64(ack[0:])),
		Total:        numADU,
	}
	return res, nil
}
