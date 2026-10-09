//go:build !accessibility || (!darwin && !windows && !linux)

package glfw

func (*window) updateAccessibility() {
}

func (*window) initAccessibilityForWindow() {
}

func (*window) cleanupAccessibilityForWindow() {
}
