package main

import (
	"os"
	"testing"
)

// TestIsTerminalDevNull checks that /dev/null, a character device but
// never a terminal, is not misreported as one.
func TestIsTerminalDevNull(t *testing.T) {
	f, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	if isTerminal(f) {
		t.Fatal("isTerminal(/dev/null) = true, want false")
	}
}

// TestIsTerminalRegularFile checks that a plain file is not a terminal
// either.
func TestIsTerminalRegularFile(t *testing.T) {
	f, err := os.CreateTemp(t.TempDir(), "isatty")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	if isTerminal(f) {
		t.Fatal("isTerminal(regular file) = true, want false")
	}
}
