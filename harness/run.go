// Package harness drives the transport arms across the loss/RTT/congestion
// matrix. ServeArm and RunArm are the per-process entry points invoked inside
// the server and client network namespaces; the orchestrator in matrix.go wires
// them together, applies netem, and sweeps the congestion controller.
package harness

import (
	"context"
	"fmt"
	"time"

	"feccliff/transport"
)

// Workload describes what a client sends. Bulk streams for Duration (capped at
// MaxBytes if nonzero) and reports goodput; deadline sends NumADU application
// data units of ADUBytes each, one per Period, and counts how many arrive within
// Deadline.
type Workload struct {
	Kind           string
	Duration       time.Duration
	MaxBytes       int64
	NumADU         int
	ADUBytes       int
	Period         time.Duration
	Deadline       time.Duration
	SymbolSize     int
	RepairPerMille int
	RateMbit       int
}

// Bulk returns a bulk workload that streams for dur, capped at maxBytes (0 means
// no cap).
func Bulk(dur time.Duration, maxBytes int64) Workload {
	return Workload{Kind: "bulk", Duration: dur, MaxBytes: maxBytes, SymbolSize: 1000, RepairPerMille: 200}
}

// Deadline returns a deadline workload.
func Deadline(numADU, aduBytes int, period, deadline time.Duration) Workload {
	return Workload{
		Kind: "deadline", NumADU: numADU, ADUBytes: aduBytes,
		Period: period, Deadline: deadline, SymbolSize: 1000, RepairPerMille: 200,
	}
}

// ServeArm binds and serves the given arm on addr until ctx is cancelled.
func ServeArm(ctx context.Context, arm, addr string) error {
	switch arm {
	case transport.ArmTCP:
		ln, err := transport.ListenTCP(addr)
		if err != nil {
			return err
		}
		return transport.ServeTCP(ctx, ln)
	case transport.ArmQUIC:
		ln, err := transport.ListenQUIC(addr)
		if err != nil {
			return err
		}
		return transport.ServeQUIC(ctx, ln)
	case transport.ArmQUICFEC:
		ln, err := transport.ListenQUIC(addr)
		if err != nil {
			return err
		}
		return transport.ServeQUICFEC(ctx, ln)
	default:
		return fmt.Errorf("unknown arm %q", arm)
	}
}

// RunArm runs one client transfer of the given arm and workload against addr.
func RunArm(arm, addr string, w Workload) (transport.Result, error) {
	switch arm {
	case transport.ArmTCP:
		if w.Kind != "bulk" {
			return transport.Result{}, fmt.Errorf("tcp arm supports bulk only")
		}
		return transport.TCPBulk(addr, w.Duration, w.MaxBytes)
	case transport.ArmQUIC:
		if w.Kind == "deadline" {
			return transport.QUICDeadline(addr, w.NumADU, w.ADUBytes, w.Period, w.Deadline)
		}
		return transport.QUICBulk(addr, w.Duration, w.MaxBytes)
	case transport.ArmQUICFEC:
		if w.Kind == "deadline" {
			return transport.Result{}, fmt.Errorf("quic-fec deadline workload not implemented")
		}
		return transport.QUICFECBulk(addr, w.Duration, w.SymbolSize, w.RepairPerMille, w.RateMbit)
	default:
		return transport.Result{}, fmt.Errorf("unknown arm %q", arm)
	}
}
