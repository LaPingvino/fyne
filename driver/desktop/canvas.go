package desktop

import "fyne.io/fyne/v2"

// Canvas defines the desktop specific extensions to a fyne.Canvas.
type Canvas interface {
	OnKeyDown() func(*fyne.KeyEvent)
	SetOnKeyDown(func(*fyne.KeyEvent))
	OnKeyUp() func(*fyne.KeyEvent)
	SetOnKeyUp(func(*fyne.KeyEvent))
}

// KeyPreviewCanvas is a desktop canvas whose application can see every key
// press before the focused widget and the shortcuts do: for keys that act
// on the whole window (Escape closing a settings window, Ctrl+PageDown
// switching its pages) whatever has the focus.
//
// Since: 2.9
type KeyPreviewCanvas interface {
	// SetOnKeyPreview sets a function that sees each key press (and
	// repeat) with its modifiers first; when it returns true the key is
	// handled and goes no further.
	SetOnKeyPreview(func(key fyne.KeyName, modifier fyne.KeyModifier) bool)
}
