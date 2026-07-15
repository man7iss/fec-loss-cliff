package transport

import (
	"context"
	"encoding/binary"
	"io"
	"time"

	"feccliff/fec"

	"github.com/quic-go/quic-go"
)

// Datagram framing on the FEC data plane. Source and repair symbols travel as
// QUIC DATAGRAMs (RFC 9221); the control plane rides a reliable stream.
const (
	dgSource      = 0
	dgRepair      = 1
	dgSourceHdr   = 1 + 4
	dgRepairHdr   = 1 + 4 + 4 + 4
	fecGeneration = 32
)

func encodeSource(sym fec.SourceSymbol) []byte {
	buf := make([]byte, dgSourceHdr+len(sym.Data))
	buf[0] = dgSource
	binary.BigEndian.PutUint32(buf[1:], sym.ESI)
	copy(buf[dgSourceHdr:], sym.Data)
	return buf
}

func encodeRepair(rep fec.RepairSymbol) []byte {
	buf := make([]byte, dgRepairHdr+len(rep.Payload))
	buf[0] = dgRepair
	binary.BigEndian.PutUint32(buf[1:], rep.RepairID)
	binary.BigEndian.PutUint32(buf[5:], rep.WindowStart)
	binary.BigEndian.PutUint32(buf[9:], rep.WindowLen)
	copy(buf[dgRepairHdr:], rep.Payload)
	return buf
}

// ServeQUICFEC serves the streaming FEC throughput arm on ln. The client streams
// source and repair datagrams for a fixed duration; the server decodes them and,
// when the client signals the end on the control stream, reports how many
// distinct source symbols it recovered. That count over the elapsed time is the
// post-recovery goodput.
func ServeQUICFEC(ctx context.Context, ln *quic.Listener) error {
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
			return aerr
		}
		go serveFECConn(ctx, conn)
	}
}

func serveFECConn(ctx context.Context, conn *quic.Conn) {
	control, err := conn.AcceptStream(ctx)
	if err != nil {
		return
	}
	mode, p1, _, _, err := readHeader(control)
	if err != nil {
		return
	}
	if mode == modeBulk {
		serveFECBulk(ctx, conn, control, int(p1))
	}
	// The client reads the count and then closes the connection.
}

func serveFECBulk(ctx context.Context, conn *quic.Conn, control *quic.Stream, symbolSize int) {
	var (
		dec  = fec.NewDecoder(symbolSize)
		have = make(map[uint32]bool)
		ack  [8]byte
	)

	dgCtx, stop := context.WithCancel(ctx)
	defer stop()
	record := func(esi uint32, _ []byte) { have[esi] = true }
	go func() {
		for {
			dg, derr := conn.ReceiveDatagram(dgCtx)
			if derr != nil {
				return
			}
			feedDatagram(dec, dg, record)
		}
	}()

	var b [1]byte
	io.ReadFull(control, b[:])
	time.Sleep(200 * time.Millisecond)
	stop()

	binary.BigEndian.PutUint64(ack[:], uint64(len(have)))
	control.Write(ack[:])
	control.Close()
}

func feedDatagram(dec *fec.Decoder, dg []byte, record func(uint32, []byte)) {
	if len(dg) < 1 {
		return
	}
	switch dg[0] {
	case dgSource:
		if len(dg) < dgSourceHdr {
			return
		}
		esi := binary.BigEndian.Uint32(dg[1:])
		data := append([]byte(nil), dg[dgSourceHdr:]...)
		dec.AddSource(fec.SourceSymbol{ESI: esi, Data: data})
		record(esi, data)
	case dgRepair:
		if len(dg) < dgRepairHdr {
			return
		}
		dec.AddRepair(fec.RepairSymbol{
			RepairID:    binary.BigEndian.Uint32(dg[1:]),
			WindowStart: binary.BigEndian.Uint32(dg[5:]),
			WindowLen:   binary.BigEndian.Uint32(dg[9:]),
			Payload:     append([]byte(nil), dg[dgRepairHdr:]...),
		})
	}
	for _, s := range dec.Recovered() {
		record(s.ESI, s.Data)
	}
}
