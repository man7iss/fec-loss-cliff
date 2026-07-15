package harness

import "testing"

func TestParseResult(t *testing.T) {
	t.Run("parses a RESULT line", func(t *testing.T) {
		out := "some log noise\nRESULT payload_bytes=20971520 wire_bytes=25564000 repair_bytes=4592000 duration_ns=4562607208 on_time=0 total=0\n"
		res, err := parseResult(out)
		if err != nil {
			t.Fatalf("parse: %v", err)
		}
		if res.PayloadBytes != 20971520 || res.WireBytes != 25564000 || res.RepairBytes != 4592000 {
			t.Fatalf("byte fields wrong: %+v", res)
		}
		if res.Duration.Nanoseconds() != 4562607208 {
			t.Fatalf("duration wrong: %v", res.Duration)
		}
		if got := res.RedundancyPct(); got < 21 || got > 23 {
			t.Fatalf("redundancy=%v, want ~21.9", got)
		}
	})

	t.Run("deadline counts", func(t *testing.T) {
		res, err := parseResult("RESULT payload_bytes=20000 wire_bytes=20000 repair_bytes=0 duration_ns=248097083 on_time=48 total=50\n")
		if err != nil {
			t.Fatalf("parse: %v", err)
		}
		if res.OnTime != 48 || res.Total != 50 {
			t.Fatalf("on-time counts wrong: %+v", res)
		}
		if got := res.OnTimeFrac(); got < 0.95 || got > 0.97 {
			t.Fatalf("ontime frac=%v, want 0.96", got)
		}
	})

	t.Run("missing RESULT line errors", func(t *testing.T) {
		if _, err := parseResult("no result here\n"); err == nil {
			t.Fatal("expected error for missing RESULT line")
		}
	})
}

func TestDefaultConditions(t *testing.T) {
	t.Run("full set includes loss sweep and named regimes", func(t *testing.T) {
		conds := defaultConditions(false)
		names := map[string]bool{}
		for _, c := range conds {
			names[c.Name] = true
		}
		for _, want := range []string{"sweep-loss0", "sweep-loss2", "perfect", "bad", "broken"} {
			if !names[want] {
				t.Fatalf("missing condition %q in %v", want, names)
			}
		}
	})

	t.Run("quick set is smaller", func(t *testing.T) {
		if len(defaultConditions(true)) >= len(defaultConditions(false)) {
			t.Fatal("quick set should be smaller than full set")
		}
	})
}
