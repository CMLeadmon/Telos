package main

import "testing"

func TestNormalizedSearchTerm(t *testing.T) {
	cases := []struct {
		in   string
		ok   bool
		want string
	}{
		{"al", true, "%al%"},
		{"Alice", true, "%alice%"},
		{"a", false, ""},                       // too short
		{"  x  ", false, ""},                   // trims to 1 char
		{string(make([]byte, 200)), false, ""}, // too long
		{"50%_off", true, `%50\%\_off%`},       // metacharacters escaped
	}
	for _, tc := range cases {
		got, ok := normalizedSearchTerm(tc.in)
		if ok != tc.ok {
			t.Errorf("normalizedSearchTerm(%q) ok=%v, want %v", tc.in, ok, tc.ok)
			continue
		}
		if ok && got != tc.want {
			t.Errorf("normalizedSearchTerm(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
