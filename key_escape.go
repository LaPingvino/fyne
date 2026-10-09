package fyne

// EscapeHandler may be implemented by an overlay (a dialog, a pop-up) that
// responds to the Escape key. While it is the top overlay of a canvas it
// sees Escape before the focused widget does; it reports whether it
// handled the key (a dialog cancelling, a pop-up closing).
//
// Since: 2.9
type EscapeHandler interface {
	HandleEscape() bool
}
