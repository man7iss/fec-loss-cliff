// Package transport implements the measurement arms the harness compares: a TCP
// baseline whose congestion controller is chosen by the kernel, a plain QUIC
// stream, and a QUIC datagram plane protected by sliding-window RLC. The FEC arm
// is RFC 9265 compliant by construction: quic-go's congestion controller sees
// the loss of a datagram-carrying packet before the receiver's FEC recovers the
// payload, so recovery never hides the loss signal. Every arm transfers a
// payload and returns a Result.
package transport

import "time"

// Arm names as they appear in result rows.
const (
	ArmTCP     = "tcp"
	ArmQUIC    = "quic"
	ArmQUICFEC = "quic-fec"
)

// Result is the outcome of one transfer.
type Result struct {
	Arm          string
	PayloadBytes int64
	WireBytes    int64
	RepairBytes  int64
	Duration     time.Duration
	OnTime       int
	Total        int
}

// GoodputMbit returns application throughput in megabits per second.
func (r Result) GoodputMbit() float64 {
	if r.Duration <= 0 {
		return 0
	}
	return float64(r.PayloadBytes) * 8 / 1e6 / r.Duration.Seconds()
}

// RedundancyPct returns repair bytes as a percentage of payload bytes, the
// on-the-wire tax the fixed-rate coder pays regardless of loss.
func (r Result) RedundancyPct() float64 {
	if r.PayloadBytes == 0 {
		return 0
	}
	return float64(r.RepairBytes) / float64(r.PayloadBytes) * 100
}

// OnTimeFrac returns the fraction of deadline application data units delivered
// before their deadline. It is only meaningful for the deadline workload.
func (r Result) OnTimeFrac() float64 {
	if r.Total == 0 {
		return 0
	}
	return float64(r.OnTime) / float64(r.Total)
}
