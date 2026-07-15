package fec

// SourceSymbol is one source symbol identified by its Encoding Symbol ID.
type SourceSymbol struct {
	ESI  uint32
	Data []byte
}

// RepairSymbol is a random linear combination of the source symbols in the
// window [WindowStart, WindowStart+WindowLen). The combination coefficients are
// not carried on the wire; the decoder regenerates them from RepairID and
// WindowLen, so a repair symbol costs one symbol payload plus a small header.
type RepairSymbol struct {
	RepairID    uint32
	WindowStart uint32
	WindowLen   uint32
	Payload     []byte
}

// coefficients derives WindowLen nonzero GF(2^8) coefficients deterministically
// from a repair symbol's RepairID, using a splitmix64 stream. The encoder and
// decoder call this with the same arguments and get the same vector, which is
// why the coefficients never travel on the wire.
func coefficients(repairID uint32, n int) []byte {
	var (
		out = make([]byte, n)
		s   = uint64(repairID) + 0x9E3779B97F4A7C15
	)

	for i := 0; i < n; i++ {
		s += 0x9E3779B97F4A7C15
		z := s
		z = (z ^ (z >> 30)) * 0xBF58476D1CE4E5B9
		z = (z ^ (z >> 27)) * 0x94D049BB133111EB
		z ^= z >> 31
		c := byte(z)
		if c == 0 {
			c = 1
		}
		out[i] = c
	}
	return out
}

// Encoder produces source and repair symbols for a sliding window of the most
// recent source symbols.
type Encoder struct {
	symbolSize int
	maxWindow  int
	window     []SourceSymbol
	nextESI    uint32
}

// NewEncoder returns an encoder for fixed-size symbols of symbolSize bytes whose
// coding window holds at most maxWindow source symbols.
func NewEncoder(symbolSize, maxWindow int) *Encoder {
	return &Encoder{
		symbolSize: symbolSize,
		maxWindow:  maxWindow,
		window:     make([]SourceSymbol, 0, maxWindow),
	}
}

// AddSource copies data into a new source symbol, slides the window forward, and
// returns the symbol. data is padded with zeros or truncated to the symbol size.
func (e *Encoder) AddSource(data []byte) SourceSymbol {
	var buf []byte

	buf = make([]byte, e.symbolSize)
	copy(buf, data)
	sym := SourceSymbol{ESI: e.nextESI, Data: buf}
	e.nextESI++
	e.window = append(e.window, sym)
	if len(e.window) > e.maxWindow {
		e.window = e.window[1:]
	}
	return sym
}

// Repair returns a repair symbol over the current window, tagged with repairID.
// The caller is responsible for issuing distinct repairID values so that repairs
// over the same window are linearly independent.
func (e *Encoder) Repair(repairID uint32) RepairSymbol {
	var (
		n       = len(e.window)
		coefs   = coefficients(repairID, n)
		payload = make([]byte, e.symbolSize)
	)

	for i, sym := range e.window {
		mulAddAssign(payload, coefs[i], sym.Data)
	}
	return RepairSymbol{
		RepairID:    repairID,
		WindowStart: e.window[0].ESI,
		WindowLen:   uint32(n),
		Payload:     payload,
	}
}

// WindowLen reports the number of source symbols currently in the coding window.
func (e *Encoder) WindowLen() int {
	return len(e.window)
}

// equation is a linear equation over the source symbols not yet recovered:
// sum(coefs[esi]*symbol[esi]) = payload. Known source symbols have already been
// folded into payload and removed from coefs.
type equation struct {
	coefs   map[uint32]byte
	payload []byte
}

// keepWindow bounds how far back the decoder retains solved symbols and pending
// equations. It is far larger than any repair coding window, so pruning never
// discards state a future repair could reference, but it keeps the working set
// (and garbage-collector pressure) constant on an unbounded stream.
const keepWindow = 4096

// Decoder recovers missing source symbols on the fly from any mix of received
// source and repair symbols. It runs incremental Gaussian elimination over
// GF(2^8) and reports newly recovered symbols as they are solved, which is not
// necessarily ESI order; the caller reassembles by ESI.
type Decoder struct {
	symbolSize int
	known      map[uint32][]byte
	eqs        []*equation
	recovered  []SourceSymbol
	maxESI     uint32
	seen       int
}

// NewDecoder returns a decoder for fixed-size symbols of symbolSize bytes.
func NewDecoder(symbolSize int) *Decoder {
	return &Decoder{
		symbolSize: symbolSize,
		known:      make(map[uint32][]byte),
	}
}

// prune drops solved symbols and stale equations older than keepWindow behind
// the highest ESI seen, bounding memory on a long stream. It runs periodically
// rather than every symbol so its cost amortizes to near zero.
func (d *Decoder) prune() {
	var kept []*equation

	if d.maxESI < keepWindow {
		return
	}
	threshold := d.maxESI - keepWindow
	for esi := range d.known {
		if esi < threshold {
			delete(d.known, esi)
		}
	}
	kept = d.eqs[:0]
	for _, eq := range d.eqs {
		esi, ok := smallestESI(eq.coefs)
		if ok && esi >= threshold {
			kept = append(kept, eq)
		}
	}
	d.eqs = kept
}

// AddSource records a received source symbol and attempts to solve for any
// symbols the new information unlocks.
func (d *Decoder) AddSource(sym SourceSymbol) {
	if _, ok := d.known[sym.ESI]; ok {
		return
	}
	buf := make([]byte, d.symbolSize)
	copy(buf, sym.Data)
	if sym.ESI > d.maxESI {
		d.maxESI = sym.ESI
	}
	d.setKnown(sym.ESI, buf)
	d.solve()
	d.maybePrune()
}

// maybePrune runs prune once every keepWindow/4 received symbols.
func (d *Decoder) maybePrune() {
	d.seen++
	if d.seen%(keepWindow/4) == 0 {
		d.prune()
	}
}

// AddRepair records a received repair symbol and attempts to solve.
func (d *Decoder) AddRepair(rep RepairSymbol) {
	var (
		coefs = coefficients(rep.RepairID, int(rep.WindowLen))
		eq    = &equation{coefs: make(map[uint32]byte), payload: make([]byte, d.symbolSize)}
	)

	copy(eq.payload, rep.Payload)
	for i := 0; i < int(rep.WindowLen); i++ {
		esi := rep.WindowStart + uint32(i)
		if data, ok := d.known[esi]; ok {
			mulAddAssign(eq.payload, coefs[i], data)
			continue
		}
		eq.coefs[esi] = coefs[i]
	}
	if rep.WindowStart+rep.WindowLen > d.maxESI {
		d.maxESI = rep.WindowStart + rep.WindowLen
	}
	if len(eq.coefs) == 0 {
		return
	}
	d.eqs = append(d.eqs, eq)
	d.solve()
	d.maybePrune()
}

// setKnown marks esi as recovered, folds it out of every pending equation, and
// queues it for the caller. It does not itself re-run the solver.
func (d *Decoder) setKnown(esi uint32, data []byte) {
	d.known[esi] = data
	d.recovered = append(d.recovered, SourceSymbol{ESI: esi, Data: data})
	if esi > d.maxESI {
		d.maxESI = esi
	}
	for _, eq := range d.eqs {
		if c, ok := eq.coefs[esi]; ok {
			mulAddAssign(eq.payload, c, data)
			delete(eq.coefs, esi)
		}
	}
}

// solve runs Gaussian elimination over the pending equations until no further
// source symbol can be determined.
func (d *Decoder) solve() {
	for {
		d.eliminate()
		progressed := false
		kept := d.eqs[:0]
		for _, eq := range d.eqs {
			switch len(eq.coefs) {
			case 0:
				continue
			case 1:
				var esi uint32
				var c byte
				for e, cc := range eq.coefs {
					esi, c = e, cc
				}
				data := make([]byte, d.symbolSize)
				mulAddAssign(data, gfInv(c), eq.payload)
				d.setKnown(esi, data)
				progressed = true
			default:
				kept = append(kept, eq)
			}
		}
		d.eqs = kept
		if !progressed {
			return
		}
	}
}

// eliminate reduces the pending equations toward row echelon form so that
// equations which jointly determine a symbol collapse to a single unknown.
func (d *Decoder) eliminate() {
	var pivots map[uint32]*equation

	pivots = make(map[uint32]*equation, len(d.eqs))
	for _, eq := range d.eqs {
		for {
			esi, ok := smallestESI(eq.coefs)
			if !ok {
				break
			}
			piv, seen := pivots[esi]
			if !seen {
				pivots[esi] = eq
				break
			}
			factor := gfMul(eq.coefs[esi], gfInv(piv.coefs[esi]))
			for e, c := range piv.coefs {
				eq.coefs[e] ^= gfMul(factor, c)
				if eq.coefs[e] == 0 {
					delete(eq.coefs, e)
				}
			}
			mulAddAssign(eq.payload, factor, piv.payload)
		}
	}
}

// smallestESI returns the lowest ESI present in coefs, or false if empty. The
// pivot row for each column is the row whose smallest unknown is that column,
// which keeps elimination deterministic.
func smallestESI(coefs map[uint32]byte) (uint32, bool) {
	var (
		best  uint32
		found bool
	)

	for esi := range coefs {
		if !found || esi < best {
			best, found = esi, true
		}
	}
	return best, found
}

// Recovered drains and returns the source symbols recovered since the last call.
func (d *Decoder) Recovered() []SourceSymbol {
	out := d.recovered
	d.recovered = nil
	return out
}

// Has reports whether the symbol with the given ESI is known.
func (d *Decoder) Has(esi uint32) bool {
	_, ok := d.known[esi]
	return ok
}
