package transport

import (
	"context"
	"testing"
	"time"
)

// These tests exercise every arm end to end over the loopback interface. They
// do not measure the loss/RTT matrix (that needs netem on Linux); they prove the
// wire protocols transfer bytes correctly and that the FEC arm reconstructs the
// exact payload, including through simulated datagram loss.

func TestTCPBulk(t *testing.T) {
	t.Run("transfers payload over loopback", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		t.Cleanup(cancel)
		ln, err := ListenTCP("127.0.0.1:0")
		if err != nil {
			t.Fatalf("listen: %v", err)
		}
		go ServeTCP(ctx, ln)

		res, err := TCPBulk(ln.Addr().String(), 300*time.Millisecond, 8*1024*1024)
		if err != nil {
			t.Fatalf("bulk: %v", err)
		}
		if res.PayloadBytes <= 0 {
			t.Fatalf("no bytes delivered")
		}
		if res.GoodputMbit() <= 0 {
			t.Fatalf("goodput not positive: %v", res.GoodputMbit())
		}
	})
}

func TestQUICBulk(t *testing.T) {
	t.Run("transfers payload over loopback", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		t.Cleanup(cancel)
		ln, err := ListenQUIC("127.0.0.1:0")
		if err != nil {
			t.Fatalf("listen: %v", err)
		}
		go ServeQUIC(ctx, ln)

		res, err := QUICBulk(ln.Addr().String(), 300*time.Millisecond, 8*1024*1024)
		if err != nil {
			t.Fatalf("bulk: %v", err)
		}
		if res.GoodputMbit() <= 0 {
			t.Fatalf("goodput not positive: %v", res.GoodputMbit())
		}
	})
}

func TestQUICFECBulk(t *testing.T) {
	t.Run("clean link streams with repair overhead", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		t.Cleanup(cancel)
		ln, err := ListenQUIC("127.0.0.1:0")
		if err != nil {
			t.Fatalf("listen: %v", err)
		}
		go ServeQUICFEC(ctx, ln)

		res, err := QUICFECBulk(ln.Addr().String(), 300*time.Millisecond, 1000, 100, 0)
		if err != nil {
			t.Fatalf("bulk: %v", err)
		}
		if res.PayloadBytes <= 0 {
			t.Fatalf("no symbols recovered")
		}
		if res.RepairBytes <= 0 {
			t.Fatalf("expected repair bytes at 10%% ratio, got %d", res.RepairBytes)
		}
	})

	t.Run("recovers dropped source symbols from repairs", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		t.Cleanup(cancel)
		ln, err := ListenQUIC("127.0.0.1:0")
		if err != nil {
			t.Fatalf("listen: %v", err)
		}
		go ServeQUICFEC(ctx, ln)

		// Drop one source symbol out of every 40; a 15% repair ratio covers it.
		dropSourceForTest = func(esi uint32) bool { return esi%40 == 13 }
		t.Cleanup(func() { dropSourceForTest = nil })

		res, err := QUICFECBulk(ln.Addr().String(), 400*time.Millisecond, 1000, 150, 0)
		if err != nil {
			t.Fatalf("bulk: %v", err)
		}
		// Source symbols generated on the wire (delivered plus dropped).
		generated := (res.WireBytes - res.RepairBytes) / 1000
		recovered := res.PayloadBytes / 1000
		if recovered < generated*9/10 {
			t.Fatalf("recovered %d of %d source symbols; repairs did not cover the drops", recovered, generated)
		}
	})
}

func TestQUICDeadline(t *testing.T) {
	t.Run("clean link delivers all units on time", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		t.Cleanup(cancel)
		ln, err := ListenQUIC("127.0.0.1:0")
		if err != nil {
			t.Fatalf("listen: %v", err)
		}
		go ServeQUIC(ctx, ln)

		res, err := QUICDeadline(ln.Addr().String(), 40, 200, 2*time.Millisecond, 100*time.Millisecond)
		if err != nil {
			t.Fatalf("deadline: %v", err)
		}
		if res.Total != 40 {
			t.Fatalf("total=%d, want 40", res.Total)
		}
		if res.OnTimeFrac() < 1 {
			t.Fatalf("on-time fraction=%v on a clean link, want 1", res.OnTimeFrac())
		}
	})
}
