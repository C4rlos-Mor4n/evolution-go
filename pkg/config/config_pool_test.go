package config

import "testing"

func TestEnvPositiveInt(t *testing.T) {
	cases := []struct {
		in   string
		def  int
		want int
	}{
		{"", 20, 20},
		{"50", 20, 50},
		{" 7 ", 20, 7},
		{"0", 20, 20},
		{"-3", 20, 20},
		{"abc", 5, 5},
	}
	for _, c := range cases {
		if got := envPositiveInt(c.in, c.def); got != c.want {
			t.Errorf("envPositiveInt(%q, %d) = %d, want %d", c.in, c.def, got, c.want)
		}
	}
}
