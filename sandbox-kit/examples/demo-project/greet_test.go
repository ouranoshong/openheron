package main

import "testing"

func TestGreeting(t *testing.T) {
	if got, want := Greeting("Ada"), "Hello, Ada!"; got != want {
		t.Errorf("Greeting(%q) = %q, want %q", "Ada", got, want)
	}
}
