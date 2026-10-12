package main

import "testing"

func TestMigrationFlags(t *testing.T) {
	for _, tc := range []struct {
		args  []string
		mode  string
		valid bool
	}{
		{nil, "", true},
		{[]string{"-migrate-only"}, "up", true},
		{[]string{"-migrate-down"}, "down", true},
		{[]string{"-migrate-only", "-migrate-down"}, "", false},
		{[]string{"-unknown"}, "", false},
		{[]string{"unexpected"}, "", false},
	} {
		mode, err := parseCommand(tc.args)
		if (err == nil) != tc.valid || mode != tc.mode {
			t.Fatalf("%v: mode=%q err=%v", tc.args, mode, err)
		}
	}
}
