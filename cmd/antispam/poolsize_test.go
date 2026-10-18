package main

import "testing"

func TestWithPoolFloor(t *testing.T) {
	tests := []struct{ in, want string }{
		{"postgres://u:p@db:5432/antispam?sslmode=disable", "postgres://u:p@db:5432/antispam?pool_max_conns=20&sslmode=disable"},
		{"postgres://u:p@db/antispam", "postgres://u:p@db/antispam?pool_max_conns=20"},
		{"postgres://u:p@db/antispam?pool_max_conns=50", "postgres://u:p@db/antispam?pool_max_conns=50"},
		{"host=db dbname=antispam", "host=db dbname=antispam pool_max_conns=20"},
		{"host=db pool_max_conns=8", "host=db pool_max_conns=8"},
		{"", ""},
	}
	for _, tt := range tests {
		if got := withPoolFloor(tt.in, minPoolConns); got != tt.want {
			t.Errorf("withPoolFloor(%q) = %q, want %q (the operator's own setting always wins)", tt.in, got, tt.want)
		}
	}
}
