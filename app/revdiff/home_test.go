package main

import (
	"fmt"
	"os"
	"testing"
)

// TestMain gives the package's tests a home folder of their own. run()
// keeps the reviewed folders, the saved connections and the open tabs under
// ~/.config/revdiff, and the tests that drive it, in this process or in a
// subprocess that inherits the environment, must never write the user's.
func TestMain(m *testing.M) {
	home, err := os.MkdirTemp("", "aidiff-test-home-")
	if err != nil {
		fmt.Fprintln(os.Stderr, "test home:", err)
		os.Exit(1)
	}
	if err := os.Setenv("HOME", home); err != nil {
		fmt.Fprintln(os.Stderr, "test home:", err)
		os.Exit(1)
	}
	code := m.Run()
	_ = os.RemoveAll(home)
	os.Exit(code)
}
