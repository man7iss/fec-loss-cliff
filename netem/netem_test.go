package netem

import (
	"strings"
	"testing"
)

func joined(argv []string) string {
	return strings.Join(argv, " ")
}

func TestNetemArgs(t *testing.T) {
	t.Run("delay is half the rtt", func(t *testing.T) {
		c := Condition{RTTMs: 100}
		got := joined(c.netemArgs())
		if !strings.Contains(got, "delay 50ms") {
			t.Fatalf("args %q missing 'delay 50ms'", got)
		}
	})

	t.Run("uniform loss", func(t *testing.T) {
		c := Condition{LossPct: 2}
		got := joined(c.netemArgs())
		if !strings.Contains(got, "loss 2%") {
			t.Fatalf("args %q missing 'loss 2%%'", got)
		}
		if strings.Contains(got, "gemodel") {
			t.Fatalf("args %q should not use gemodel", got)
		}
	})

	t.Run("gilbert-elliott loss", func(t *testing.T) {
		c := Condition{LossPct: 2, Gemodel: true, BurstLen: 4}
		got := joined(c.netemArgs())
		if !strings.Contains(got, "loss gemodel") {
			t.Fatalf("args %q missing 'loss gemodel'", got)
		}
	})

	t.Run("rate and jitter", func(t *testing.T) {
		c := Condition{RTTMs: 20, JitterMs: 5, RateMbit: 100}
		got := joined(c.netemArgs())
		if !strings.Contains(got, "distribution normal") {
			t.Fatalf("args %q missing jitter distribution", got)
		}
		if !strings.Contains(got, "rate 100mbit") {
			t.Fatalf("args %q missing 'rate 100mbit'", got)
		}
	})

	t.Run("clean link has no loss or rate", func(t *testing.T) {
		c := Condition{RTTMs: 2}
		got := joined(c.netemArgs())
		if strings.Contains(got, "loss") || strings.Contains(got, "rate") {
			t.Fatalf("clean args %q should have neither loss nor rate", got)
		}
	})
}

func TestSetupCommands(t *testing.T) {
	c := Condition{Name: "bad", RateMbit: 50, RTTMs: 80, LossPct: 2}
	cmds := c.SetupCommands()

	t.Run("creates both namespaces", func(t *testing.T) {
		var haveCli, haveSrv bool
		for _, argv := range cmds {
			j := joined(argv)
			if j == "ip netns add "+NSClient {
				haveCli = true
			}
			if j == "ip netns add "+NSServer {
				haveSrv = true
			}
		}
		if !haveCli || !haveSrv {
			t.Fatalf("missing namespace creation: cli=%v srv=%v", haveCli, haveSrv)
		}
	})

	t.Run("applies netem on both veth ends", func(t *testing.T) {
		var cliQdisc, srvQdisc bool
		for _, argv := range cmds {
			j := joined(argv)
			if strings.Contains(j, "tc qdisc add dev "+VethClient) && strings.Contains(j, "loss 2%") {
				cliQdisc = true
			}
			if strings.Contains(j, "tc qdisc add dev "+VethServer) && strings.Contains(j, "loss 2%") {
				srvQdisc = true
			}
		}
		if !cliQdisc || !srvQdisc {
			t.Fatalf("netem not applied symmetrically: cli=%v srv=%v", cliQdisc, srvQdisc)
		}
	})

	t.Run("veth peer created before being moved into namespaces", func(t *testing.T) {
		addIdx, moveIdx := -1, -1
		for i, argv := range cmds {
			j := joined(argv)
			if strings.HasPrefix(j, "ip link add "+VethClient) {
				addIdx = i
			}
			if strings.HasPrefix(j, "ip link set "+VethClient+" netns") {
				moveIdx = i
			}
		}
		if addIdx < 0 || moveIdx < 0 || addIdx > moveIdx {
			t.Fatalf("veth add/move ordering wrong: add=%d move=%d", addIdx, moveIdx)
		}
	})
}

func TestTeardownCommands(t *testing.T) {
	t.Run("removes namespaces and veth", func(t *testing.T) {
		cmds := TeardownCommands()
		if len(cmds) != 3 {
			t.Fatalf("teardown has %d commands, want 3", len(cmds))
		}
		if joined(cmds[0]) != "ip netns del "+NSClient {
			t.Fatalf("unexpected first teardown command: %q", joined(cmds[0]))
		}
	})
}
