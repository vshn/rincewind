package tui

import (
	"testing"

	"github.com/vshn/rincewind/internal/config"
)

// The reserved set in config and the switch in onKey are two halves of one
// contract, kept in step by hand. This is what fails when only one of them
// is edited: a key the tool answers but does not reserve is one a hat can
// bind and then wonder why nothing happens, and a key reserved but unbound
// is one taken from the views for nothing.
func TestBoundKeysAndReservedKeysAgree(t *testing.T) {
	reserved := map[string]bool{}
	for _, key := range config.ReservedKeys() {
		reserved[key] = true
	}
	bound := map[string]bool{}
	for _, key := range boundKeys {
		bound[key] = true
	}

	for _, key := range boundKeys {
		// enter is the deliberate exception; see boundKeys.
		if key == "enter" {
			continue
		}
		if !reserved[key] {
			t.Errorf("the tool answers %q but config does not reserve it", key)
		}
	}
	for _, key := range config.ReservedKeys() {
		if !bound[key] {
			t.Errorf("config reserves %q, which nothing binds", key)
		}
	}
}
