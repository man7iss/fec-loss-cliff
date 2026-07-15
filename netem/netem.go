// Package netem builds and applies the Linux network-emulation topology the
// harness runs over: two network namespaces joined by a veth pair, with netem
// applied symmetrically on each end. Command construction is pure and testable;
// Setup and Teardown shell out to ip and tc and therefore only run on Linux.
package netem

import (
	"fmt"
	"os/exec"
	"strings"
)

const (
	NSClient   = "feccli"
	NSServer   = "fecsrv"
	VethClient = "veth-cli"
	VethServer = "veth-srv"
	ClientIP   = "10.7.0.1"
	ServerIP   = "10.7.0.2"
	Prefix     = "24"
)

// Condition describes one cell of the loss/RTT/bandwidth matrix. RTT is the
// round-trip time; half is applied as one-way delay on each veth end so the two
// directions sum to RTT. Loss, reorder, and duplicate are per-direction
// percentages applied on both ends.
type Condition struct {
	Name       string
	RateMbit   int
	RTTMs      float64
	JitterMs   float64
	LossPct    float64
	ReorderPct float64
	DupPct     float64
	Gemodel    bool
	BurstLen   float64
}

// netemArgs returns the netem qdisc arguments for one veth end. A Gilbert-Elliott
// loss model is used when Gemodel is set, with the good-to-bad and bad-to-good
// transition probabilities derived from LossPct and the mean burst length.
func (c Condition) netemArgs() []string {
	var args []string

	args = append(args, "netem")
	if c.RTTMs > 0 {
		args = append(args, "delay", fmt.Sprintf("%.3gms", c.RTTMs/2))
		if c.JitterMs > 0 {
			args = append(args, fmt.Sprintf("%.3gms", c.JitterMs), "distribution", "normal")
		}
	}
	if c.LossPct > 0 {
		if c.Gemodel {
			burst := c.BurstLen
			if burst < 1 {
				burst = 1
			}
			r := 100 / burst
			p := c.LossPct * r / (100 - c.LossPct)
			args = append(args, "loss", "gemodel", fmt.Sprintf("%.4g%%", p), fmt.Sprintf("%.4g%%", r))
		} else {
			args = append(args, "loss", fmt.Sprintf("%.4g%%", c.LossPct))
		}
	}
	if c.ReorderPct > 0 {
		args = append(args, "reorder", fmt.Sprintf("%.4g%%", c.ReorderPct))
	}
	if c.DupPct > 0 {
		args = append(args, "duplicate", fmt.Sprintf("%.4g%%", c.DupPct))
	}
	if c.RateMbit > 0 {
		args = append(args, "rate", fmt.Sprintf("%dmbit", c.RateMbit))
	}
	return args
}

// SetupCommands returns the ordered argv sequences that build the topology and
// apply the condition. Each element is a full command; argv[0] is the program.
func (c Condition) SetupCommands() [][]string {
	var cmds [][]string

	cmds = append(cmds,
		[]string{"ip", "netns", "add", NSClient},
		[]string{"ip", "netns", "add", NSServer},
		[]string{"ip", "link", "add", VethClient, "type", "veth", "peer", "name", VethServer},
		[]string{"ip", "link", "set", VethClient, "netns", NSClient},
		[]string{"ip", "link", "set", VethServer, "netns", NSServer},
		[]string{"ip", "netns", "exec", NSClient, "ip", "addr", "add", ClientIP + "/" + Prefix, "dev", VethClient},
		[]string{"ip", "netns", "exec", NSServer, "ip", "addr", "add", ServerIP + "/" + Prefix, "dev", VethServer},
		[]string{"ip", "netns", "exec", NSClient, "ip", "link", "set", VethClient, "up"},
		[]string{"ip", "netns", "exec", NSServer, "ip", "link", "set", VethServer, "up"},
		[]string{"ip", "netns", "exec", NSClient, "ip", "link", "set", "lo", "up"},
		[]string{"ip", "netns", "exec", NSServer, "ip", "link", "set", "lo", "up"},
	)
	cliQdisc := append([]string{"ip", "netns", "exec", NSClient, "tc", "qdisc", "add", "dev", VethClient, "root"}, c.netemArgs()...)
	srvQdisc := append([]string{"ip", "netns", "exec", NSServer, "tc", "qdisc", "add", "dev", VethServer, "root"}, c.netemArgs()...)
	cmds = append(cmds, cliQdisc, srvQdisc)
	return cmds
}

// TeardownCommands returns the argv sequences that remove the topology. They are
// safe to run even if setup only partially completed.
func TeardownCommands() [][]string {
	return [][]string{
		{"ip", "netns", "del", NSClient},
		{"ip", "netns", "del", NSServer},
		{"ip", "link", "del", VethClient},
	}
}

// Setup builds the topology and applies the condition, stopping at the first
// command that fails.
func (c Condition) Setup() error {
	for _, argv := range c.SetupCommands() {
		if err := run(argv); err != nil {
			return fmt.Errorf("netem setup %q: %w", strings.Join(argv, " "), err)
		}
	}
	return nil
}

// Teardown removes the topology, ignoring errors from commands whose target was
// never created.
func Teardown() {
	for _, argv := range TeardownCommands() {
		_ = run(argv)
	}
}

func run(argv []string) error {
	cmd := exec.Command(argv[0], argv[1:]...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}
