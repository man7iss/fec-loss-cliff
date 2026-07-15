package transport

import (
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"time"

	"feccliff/fec"

	"github.com/quic-go/quic-go"
)

// dropSourceForTest, when non-nil, suppresses sending the source datagram for
// the given symbol index so tests can exercise repair-based recovery in the arm
// without a network emulator. It is nil in normal operation.
var dropSourceForTest func(uint32) bool

// QUICFECBulk streams source and repair datagrams over the QUIC datagram plane
// for up to maxDur at repairPerMille repair symbols per thousand source symbols,
// then asks the receiver how many distinct source symbols it recovered. Goodput
// is that count times the symbol size over the elapsed time; RepairBytes records
// the coding overhead put on the wire. Generation is paced to rateMbit (the link
// rate) because quic-go's SendDatagram silently drops frames once its send queue
// is full, which would otherwise discard most source symbols on a fast link.
func QUICFECBulk(addr string, maxDur time.Duration, symbolSize, repairPerMille, rateMbit int) (Result, error) {
	var (
		res         Result
		enc         = fec.NewEncoder(symbolSize, fecGeneration)
		chunk       = make([]byte, symbolSize)
		ack         [8]byte
		sourceBytes int64
		repairBytes int64
		paceBytes   int64
		repairID    uint32
		esi         uint32
	)

	ctx := context.Background()
	conn, err := quic.DialAddr(ctx, addr, clientTLS(), quicConfig())
	if err != nil {
		return res, fmt.Errorf("quic-fec dial %s: %w", addr, err)
	}
	defer conn.CloseWithError(0, "")
	control, err := conn.OpenStreamSync(ctx)
	if err != nil {
		return res, fmt.Errorf("quic-fec open control: %w", err)
	}
	if _, err = control.Write(putHeader(modeBulk, uint64(symbolSize), 0, 0)); err != nil {
		return res, fmt.Errorf("quic-fec write header: %w", err)
	}

	start := time.Now()
	deadline := start.Add(maxDur)
	pace := func() {
		if rateMbit <= 0 {
			return
		}
		target := time.Duration(float64(paceBytes) * 8 / float64(rateMbit) / 1e6 * float64(time.Second))
		// Only sleep once we are meaningfully ahead of schedule; sleeping after
		// every datagram would spend more time in the scheduler than on the wire.
		if d := target - time.Since(start); d > 2*time.Millisecond {
			time.Sleep(d)
		}
	}
	for time.Now().Before(deadline) {
		sym := enc.AddSource(chunk)
		if dropSourceForTest == nil || !dropSourceForTest(esi) {
			if serr := conn.SendDatagram(encodeSource(sym)); serr != nil {
				break
			}
			paceBytes += int64(symbolSize)
		}
		sourceBytes += int64(symbolSize)
		esi++
		pace()
		if esi%fecGeneration == 0 {
			for r := 0; r < repairCount(enc.WindowLen(), repairPerMille); r++ {
				rep := enc.Repair(repairID)
				repairID++
				if serr := conn.SendDatagram(encodeRepair(rep)); serr != nil {
					break
				}
				repairBytes += int64(symbolSize)
				paceBytes += int64(symbolSize)
				pace()
			}
		}
	}
	elapsed := time.Since(start)

	if _, err = control.Write([]byte{1}); err != nil {
		return res, fmt.Errorf("quic-fec write fin: %w", err)
	}
	control.SetReadDeadline(time.Now().Add(30 * time.Second))
	if _, err = io.ReadFull(control, ack[:]); err != nil {
		return res, fmt.Errorf("quic-fec read count: %w", err)
	}
	recovered := int64(binary.BigEndian.Uint64(ack[:]))
	res = Result{
		Arm:          ArmQUICFEC,
		PayloadBytes: recovered * int64(symbolSize),
		WireBytes:    sourceBytes + repairBytes,
		RepairBytes:  repairBytes,
		Duration:     elapsed,
	}
	return res, nil
}

// repairCount returns how many repair symbols to emit for a generation window of
// n source symbols at perMille repair symbols per thousand, rounding up so a
// nonzero ratio always yields at least one repair.
func repairCount(n, perMille int) int {
	if perMille <= 0 || n == 0 {
		return 0
	}
	r := (n*perMille + 999) / 1000
	if r < 1 {
		r = 1
	}
	return r
}
