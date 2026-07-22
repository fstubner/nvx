package main

import "testing"

func TestUsePersistentProfile(t *testing.T) {
	if usePersistentProfile("") {
		t.Fatal("no tool name -> ephemeral profile")
	}
	if !usePersistentProfile("wrangler") {
		t.Fatal("granted tool name -> persistent profile")
	}
}
