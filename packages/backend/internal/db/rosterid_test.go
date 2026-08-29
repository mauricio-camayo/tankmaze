package db

import "testing"

func TestRealTankID(t *testing.T) {
	cases := []struct{ in, want string }{
		{"builtin-scout", "builtin-scout"},                     // unsuffixed — common case, no-op
		{"builtin-scout#2", "builtin-scout"},                   // autofill duplicate (item 248)
		{"builtin-scout#12", "builtin-scout"},                  // double-digit occurrence count
		{"4db4baec-fef9-41f8-9354", "4db4baec-fef9-41f8-9354"}, // real (non-AI) tankId, untouched
	}
	for _, c := range cases {
		if got := RealTankID(c.in); got != c.want {
			t.Errorf("RealTankID(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
