package browser

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestClose_NilState verifies that Close() on a zero-value Launcher does not panic.
// This is a defensive test for partially-initialized launchers (e.g., if NewLauncher
// fails partway through initialization).
func TestClose_NilState(t *testing.T) {
	l := &Launcher{}
	assert.NotPanics(t, func() {
		l.Close()
	})
}

// TestIsConnected_NilBrowser verifies that IsConnected returns false when browser is nil.
func TestIsConnected_NilBrowser(t *testing.T) {
	l := &Launcher{}
	assert.False(t, l.IsConnected())
}
