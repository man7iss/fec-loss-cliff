// Package fec implements a sliding-window Random Linear Code (RLC) over GF(2^8),
// the forward-erasure-correction scheme used to protect the QUIC DATAGRAM and
// raw-UDP data planes in this harness. The field and coding style follow the
// AL-FEC schemes in RFC 5510 and RFC 8681.
package fec

const gfPoly = 0x11D

var (
	gfExp [512]byte
	gfLog [256]byte
)

// init builds the log/exp tables for GF(2^8) with primitive polynomial 0x11D
// and generator 2. The exp table is doubled so a product of two logs can be
// looked up without a modular reduction.
func init() {
	var x int

	x = 1
	for i := 0; i < 255; i++ {
		gfExp[i] = byte(x)
		gfLog[x] = byte(i)
		x <<= 1
		if x&0x100 != 0 {
			x ^= gfPoly
		}
	}
	for i := 255; i < 512; i++ {
		gfExp[i] = gfExp[i-255]
	}
}

// gfMul returns a*b in GF(2^8).
func gfMul(a, b byte) byte {
	if a == 0 || b == 0 {
		return 0
	}
	return gfExp[int(gfLog[a])+int(gfLog[b])]
}

// gfInv returns the multiplicative inverse of a in GF(2^8). a must be nonzero.
func gfInv(a byte) byte {
	return gfExp[255-int(gfLog[a])]
}

// addAssign computes dst ^= src elementwise. Addition in GF(2^8) is XOR.
func addAssign(dst, src []byte) {
	for i := range dst {
		dst[i] ^= src[i]
	}
}

// mulAddAssign computes dst ^= c*src elementwise.
func mulAddAssign(dst []byte, c byte, src []byte) {
	if c == 0 {
		return
	}
	if c == 1 {
		addAssign(dst, src)
		return
	}
	base := int(gfLog[c])
	for i := range dst {
		s := src[i]
		if s != 0 {
			dst[i] ^= gfExp[base+int(gfLog[s])]
		}
	}
}
