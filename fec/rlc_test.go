package fec

import (
	"bytes"
	"testing"
)

// buildBlock encodes k source symbols into a single full-window block and
// returns the source symbols plus numRepair repair symbols over that block.
func buildBlock(symbolSize, k, numRepair int) ([]SourceSymbol, []RepairSymbol) {
	var (
		enc     = NewEncoder(symbolSize, k)
		srcs    = make([]SourceSymbol, k)
		repairs = make([]RepairSymbol, numRepair)
	)

	for i := 0; i < k; i++ {
		data := make([]byte, symbolSize)
		for j := range data {
			data[j] = byte(i*31 + j*7 + 1)
		}
		srcs[i] = enc.AddSource(data)
	}
	for r := 0; r < numRepair; r++ {
		repairs[r] = enc.Repair(uint32(r))
	}
	return srcs, repairs
}

// feed delivers the surviving source symbols and all repair symbols to a fresh
// decoder, dropping the source symbols whose index is in drop.
func feed(symbolSize int, srcs []SourceSymbol, repairs []RepairSymbol, drop map[int]bool) *Decoder {
	dec := NewDecoder(symbolSize)
	for i, s := range srcs {
		if drop[i] {
			continue
		}
		dec.AddSource(s)
	}
	for _, rp := range repairs {
		dec.AddRepair(rp)
	}
	return dec
}

func TestEncoder(t *testing.T) {
	t.Run("window slides at capacity", func(t *testing.T) {
		enc := NewEncoder(8, 4)
		for i := 0; i < 10; i++ {
			enc.AddSource([]byte{byte(i)})
		}
		if enc.WindowLen() != 4 {
			t.Fatalf("window len=%d, want 4", enc.WindowLen())
		}
		rep := enc.Repair(0)
		if rep.WindowStart != 6 || rep.WindowLen != 4 {
			t.Fatalf("repair window=[%d,+%d), want [6,+4)", rep.WindowStart, rep.WindowLen)
		}
	})

	t.Run("source is padded to symbol size", func(t *testing.T) {
		enc := NewEncoder(16, 4)
		sym := enc.AddSource([]byte{1, 2, 3})
		if len(sym.Data) != 16 {
			t.Fatalf("symbol size=%d, want 16", len(sym.Data))
		}
		for i := 3; i < 16; i++ {
			if sym.Data[i] != 0 {
				t.Fatalf("byte %d not zero-padded", i)
			}
		}
	})
}

func TestDecoder(t *testing.T) {
	const symbolSize = 64

	assertRecovered := func(t *testing.T, dec *Decoder, srcs []SourceSymbol, drop map[int]bool) {
		t.Helper()
		for i, s := range srcs {
			if !dec.Has(s.ESI) {
				t.Fatalf("symbol %d (esi %d) not recovered", i, s.ESI)
			}
			if !bytes.Equal(dec.known[s.ESI], s.Data) {
				t.Fatalf("symbol %d recovered with wrong data", i)
			}
		}
		_ = drop
	}

	t.Run("no loss decodes trivially", func(t *testing.T) {
		srcs, repairs := buildBlock(symbolSize, 16, 0)
		dec := feed(symbolSize, srcs, repairs, nil)
		assertRecovered(t, dec, srcs, nil)
	})

	t.Run("recovers losses within repair budget", func(t *testing.T) {
		srcs, repairs := buildBlock(symbolSize, 16, 4)
		drop := map[int]bool{2: true, 5: true, 9: true, 13: true}
		dec := feed(symbolSize, srcs, repairs, drop)
		assertRecovered(t, dec, srcs, drop)
	})

	t.Run("recovers with slack repairs", func(t *testing.T) {
		srcs, repairs := buildBlock(symbolSize, 32, 8)
		drop := map[int]bool{0: true, 1: true, 7: true, 15: true, 31: true}
		dec := feed(symbolSize, srcs, repairs, drop)
		assertRecovered(t, dec, srcs, drop)
	})

	t.Run("recovered list is drained once", func(t *testing.T) {
		srcs, repairs := buildBlock(symbolSize, 16, 4)
		drop := map[int]bool{3: true, 8: true}
		dec := feed(symbolSize, srcs, repairs, drop)
		got := dec.Recovered()
		gotESI := make(map[uint32]bool)
		for _, s := range got {
			gotESI[s.ESI] = true
		}
		if !gotESI[srcs[3].ESI] || !gotESI[srcs[8].ESI] {
			t.Fatalf("dropped symbols not in recovered drain: %v", gotESI)
		}
		if len(dec.Recovered()) != 0 {
			t.Fatal("second drain should be empty")
		}
	})

	t.Run("cannot recover beyond budget", func(t *testing.T) {
		srcs, repairs := buildBlock(symbolSize, 16, 2)
		drop := map[int]bool{1: true, 4: true, 10: true}
		dec := feed(symbolSize, srcs, repairs, drop)
		missing := 0
		for i, s := range srcs {
			if drop[i] && !dec.Has(s.ESI) {
				missing++
			}
		}
		if missing == 0 {
			t.Fatal("expected at least one unrecoverable symbol with 3 losses and 2 repairs")
		}
	})
}
