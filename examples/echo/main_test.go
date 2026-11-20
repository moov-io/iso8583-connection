package main

import "testing"

func TestEcho(t *testing.T) {
	t.Parallel()
	if err := run(); err != nil {
		t.Fatal(err)
	}
}
