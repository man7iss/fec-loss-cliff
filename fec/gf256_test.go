package fec

import "testing"

func TestGF256(t *testing.T) {
	t.Run("multiply identity and zero", func(t *testing.T) {
		for a := 0; a < 256; a++ {
			if got := gfMul(byte(a), 1); got != byte(a) {
				t.Fatalf("gfMul(%d,1)=%d, want %d", a, got, a)
			}
			if got := gfMul(byte(a), 0); got != 0 {
				t.Fatalf("gfMul(%d,0)=%d, want 0", a, got)
			}
		}
	})

	t.Run("multiply is commutative", func(t *testing.T) {
		for a := 0; a < 256; a++ {
			for b := 0; b < 256; b++ {
				if gfMul(byte(a), byte(b)) != gfMul(byte(b), byte(a)) {
					t.Fatalf("gfMul not commutative at (%d,%d)", a, b)
				}
			}
		}
	})

	t.Run("inverse", func(t *testing.T) {
		for a := 1; a < 256; a++ {
			if got := gfMul(byte(a), gfInv(byte(a))); got != 1 {
				t.Fatalf("a*inv(a)=%d for a=%d, want 1", got, a)
			}
		}
	})

	t.Run("distributive over addition", func(t *testing.T) {
		for a := 0; a < 256; a += 7 {
			for b := 0; b < 256; b += 11 {
				for c := 0; c < 256; c += 13 {
					lhs := gfMul(byte(a), byte(b)^byte(c))
					rhs := gfMul(byte(a), byte(b)) ^ gfMul(byte(a), byte(c))
					if lhs != rhs {
						t.Fatalf("distributivity fails at (%d,%d,%d)", a, b, c)
					}
				}
			}
		}
	})

	t.Run("mulAddAssign matches scalar", func(t *testing.T) {
		dst := []byte{1, 2, 3, 4}
		src := []byte{10, 20, 30, 40}
		mulAddAssign(dst, 7, src)
		for i := range dst {
			want := []byte{1, 2, 3, 4}[i] ^ gfMul(7, src[i])
			if dst[i] != want {
				t.Fatalf("mulAddAssign[%d]=%d, want %d", i, dst[i], want)
			}
		}
	})
}
