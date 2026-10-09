//go:build accessibility && darwin

package glfw

import (
	"context"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// The macOS bridge read as VoiceOver reads it, in a probe compiled with it
// on its own (no window, no accessibility permission needed): roles,
// labels, the text by line and caret, elements kept across snapshots and
// dropped when gone, requests handed back.
func TestAccessibilityMacElements(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	binary := filepath.Join(t.TempDir(), "probe")
	cmd := exec.CommandContext(ctx, "clang", "-Wno-deprecated-declarations", "-I.", "-framework", "Cocoa",
		"testdata/accessibility_text.m", "accessibility_darwin.m", "-o", binary)
	output, err := cmd.CombinedOutput()
	require.NoError(t, err, "%s", output)
	output, err = exec.CommandContext(ctx, binary).CombinedOutput()
	require.NoError(t, err, "%s", output)
}
