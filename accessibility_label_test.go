package fyne

import "testing"

type namedObject struct {
	CanvasObject
	label string
}

func (n *namedObject) AccessibilityLabel() string        { return n.label }
func (n *namedObject) AccessibilityRole() AccessibleRole { return AccessibleRoleButton }

func TestAccessibleLabel(t *testing.T) {
	o := &namedObject{label: "foreground_folder.svg"}
	if AccessibleLabel(o) != "foreground_folder.svg" || HasAccessibleLabel(o) {
		t.Error("own label")
	}
	SetAccessibleLabel(o, "Choose a folder")
	if AccessibleLabel(o) != "Choose a folder" || !HasAccessibleLabel(o) {
		t.Error("label set by the application")
	}
	SetAccessibleLabel(o, "")
	if AccessibleLabel(o) != "foreground_folder.svg" {
		t.Error("label removed")
	}
	if AccessibleLabel(&struct{ CanvasObject }{}) != "" {
		t.Error("an object that is not accessible")
	}
}

func TestAccessibilityAutomatic(t *testing.T) {
	t.Setenv("FYNE_ACCESSIBILITY_AUTO", "1")
	if !AccessibilityAutomatic() {
		t.Error("the environment variable")
	}
	SetAccessibilityAutomatic(false)
	defer func() {
		accessibilityAutomatic.Lock()
		accessibilityAutomatic.set = false
		accessibilityAutomatic.Unlock()
	}()
	if AccessibilityAutomatic() {
		t.Error("switched off by the application")
	}
}
