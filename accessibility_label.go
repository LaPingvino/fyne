package fyne

import (
	"os"
	"sync"
)

// Labels set by the application, and the automatic mode: what assistive
// technologies call an object, when its own label is not enough.

var accessibleLabels sync.Map // CanvasObject -> string

// SetAccessibleLabel gives obj the label assistive technologies use for it
// in place of its own: a field named after the text beside it ("Size"), an
// icon button after what it does ("Choose a folder"). An empty label
// removes it.
//
// Since: 2.9
func SetAccessibleLabel(obj CanvasObject, label string) {
	if label == "" {
		accessibleLabels.Delete(obj)
		return
	}
	accessibleLabels.Store(obj, label)
}

// AccessibleLabel is the label assistive technologies use for obj: the one
// set with SetAccessibleLabel, otherwise its own ([Accessible]), otherwise "".
//
// Since: 2.9
func AccessibleLabel(obj CanvasObject) string {
	if l, ok := accessibleLabels.Load(obj); ok {
		return l.(string)
	}
	if a, ok := obj.(Accessible); ok {
		return a.AccessibilityLabel()
	}
	return ""
}

// HasAccessibleLabel reports whether the application set obj's label.
//
// Since: 2.9
func HasAccessibleLabel(obj CanvasObject) bool {
	_, ok := accessibleLabels.Load(obj)
	return ok
}

var accessibilityAutomatic = struct {
	sync.RWMutex
	on, set bool
}{}

// SetAccessibilityAutomatic switches the automatic mode on or off: assistive
// technologies then get names the application did not give, guessed from
// how it is laid out (a field without a name takes the label before it).
// It helps an application that was not written with screen readers in
// mind; names set with SetAccessibleLabel and the widgets' own come first.
// The environment variable FYNE_ACCESSIBILITY_AUTO=1 switches it on too.
//
// Since: 2.9
func SetAccessibilityAutomatic(on bool) {
	accessibilityAutomatic.Lock()
	defer accessibilityAutomatic.Unlock()
	accessibilityAutomatic.on, accessibilityAutomatic.set = on, true
}

// AccessibilityAutomatic reports whether the automatic mode is on.
//
// Since: 2.9
func AccessibilityAutomatic() bool {
	accessibilityAutomatic.RLock()
	defer accessibilityAutomatic.RUnlock()
	if accessibilityAutomatic.set {
		return accessibilityAutomatic.on
	}
	return os.Getenv("FYNE_ACCESSIBILITY_AUTO") == "1"
}
