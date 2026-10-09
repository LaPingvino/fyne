//go:build accessibility && darwin

package glfw

/*
#cgo CFLAGS: -x objective-c
#cgo LDFLAGS: -framework Foundation -framework AppKit -framework ApplicationServices

#include <stdlib.h>
#include "accessibility_darwin.h"
*/
import "C"

import (
	"slices"
	"unsafe"

	"fyne.io/fyne/v2"
)

// The macOS bridge: the snapshots (accessibility_snapshot.go) as
// NSAccessibility elements (accessibility_darwin.m), for VoiceOver and
// braille through it. NSString counts text in UTF-16 code units; Fyne in
// runes: offsets are converted both ways here.

func a11yWindowHandle(w *window) uintptr {
	return uintptr(unsafe.Pointer(w.view().GetCocoaWindow()))
}

func a11yConnect(string) a11yPlatform { return macPlatform{} }

type macPlatform struct{}

func (macPlatform) Update(nodes []a11yNode) {
	handles := map[uint64]uintptr{}
	parents := map[uint64]uint64{}
	for _, n := range nodes {
		if n.Window {
			handles[n.ID] = n.Handle
		}
		parents[n.ID] = n.Parent
	}
	windowOf := func(id uint64) uintptr {
		for i := 0; i < 1000 && parents[id] != 0; i++ {
			id = parents[id]
		}
		return handles[id]
	}

	C.MacA11yBegin()
	for _, n := range nodes {
		role := string(n.Role)
		if n.Window {
			role = "window"
		}
		cRole, cName := C.CString(role), C.CString(n.Name)
		var children *C.ulonglong
		if len(n.Children) > 0 {
			children = (*C.ulonglong)(unsafe.Pointer(&n.Children[0]))
		}
		var cText *C.char
		caret, start, end := C.int(-1), C.int(0), C.int(0)
		var lines []C.int
		if n.Text != nil {
			cText = C.CString(n.Text.Content)
			at := utf16Offsets(n.Text.Content)
			if n.Text.Caret >= 0 {
				caret = C.int(at(n.Text.Caret))
			}
			start, end = C.int(at(n.Text.SelectionStart)), C.int(at(n.Text.SelectionEnd))
			for _, l := range n.Text.Lines {
				lines = append(lines, C.int(at(l)))
			}
		}
		var cLines *C.int
		if len(lines) > 0 {
			cLines = &lines[0]
		}
		C.MacA11yNode(C.ulonglong(n.ID), C.ulonglong(n.Parent), C.ulonglong(windowOf(n.ID)), cRole, cName,
			C.double(n.Bounds.X), C.double(n.Bounds.Y), C.double(n.Bounds.Width), C.double(n.Bounds.Height),
			C.int(macFlags(n)), children, C.int(len(n.Children)), cText, caret, start, end, cLines, C.int(len(lines)))
		C.free(unsafe.Pointer(cRole))
		C.free(unsafe.Pointer(cName))
		if cText != nil {
			C.free(unsafe.Pointer(cText))
		}
	}
	C.MacA11yEnd()
}

// macFlags are an object's states and actions for its element.
func macFlags(n a11yNode) int {
	f := 0
	set := func(on bool, flag int) {
		if on {
			f |= flag
		}
	}
	has := func(a fyne.AccessibleAction) bool { return slices.Contains(n.Actions, string(a)) }
	set(n.Focusable, C.MacA11yFocusable)
	set(n.Disabled, C.MacA11yDisabled)
	set((n.Role == fyne.AccessibleRoleTextField || n.Role == fyne.AccessibleRoleTextArea) && !n.Disabled, C.MacA11yEditable)
	set(n.Role == fyne.AccessibleRoleTextArea, C.MacA11yMultiLine)
	set(has(fyne.AccessibleActionPress), C.MacA11yPress)
	set(has(fyne.AccessibleActionIncrement), C.MacA11yIncrement)
	set(has(fyne.AccessibleActionDecrement), C.MacA11yDecrement)
	set(has(fyne.AccessibleActionShowMenu), C.MacA11yShowMenu)
	set(has(fyne.AccessibleActionSetValue) || n.Text != nil && !n.Disabled &&
		(n.Role == fyne.AccessibleRoleTextField || n.Role == fyne.AccessibleRoleTextArea), C.MacA11ySetValue)
	set(slices.Contains(n.States, fyne.AccessibleStateChecked), C.MacA11yChecked)
	set(slices.Contains(n.States, fyne.AccessibleStateSelected), C.MacA11ySelected)
	set(slices.Contains(n.States, fyne.AccessibleStateExpanded), C.MacA11yExpanded)
	return f
}

func (macPlatform) Focused(id uint64)                       { C.MacA11yFocus(C.ulonglong(id)) }
func (macPlatform) WindowActivated(uint64, bool)            {} // AppKit tells it of its windows
func (macPlatform) TextInserted(id uint64, _ int, _ string) { C.MacA11yTextChanged(C.ulonglong(id)) }
func (macPlatform) TextDeleted(id uint64, _ int, _ string)  { C.MacA11yTextChanged(C.ulonglong(id)) }

// CaretMoved: the caret is the (empty) selected text range; VoiceOver
// follows it from the selected text changing.
func (macPlatform) CaretMoved(id uint64, _ int) { C.MacA11ySelectionChanged(C.ulonglong(id)) }
func (macPlatform) SelectionChanged(id uint64)  { C.MacA11ySelectionChanged(C.ulonglong(id)) }
func (macPlatform) ForgetWindow(handle uintptr) { C.MacA11yForgetWindow(C.ulonglong(handle)) }

// The requests of assistive technologies (on the main thread: AppKit's).

//export fyneA11yAction
func fyneA11yAction(id C.ulonglong, action C.int) {
	act := [...]fyne.AccessibleAction{fyne.AccessibleActionPress, fyne.AccessibleActionIncrement,
		fyne.AccessibleActionDecrement, fyne.AccessibleActionShowMenu}
	if action < 0 || int(action) >= len(act) {
		return
	}
	obj, _ := a11yObject(uint64(id))
	if a, ok := obj.(fyne.AccessibleActions); ok {
		fyne.Do(func() { a.AccessibilityPerformAction(act[action]) })
	}
}

//export fyneA11yFocus
func fyneA11yFocus(id C.ulonglong) { a11yGrabFocus(uint64(id)) }

//export fyneA11ySelect
func fyneA11ySelect(id C.ulonglong, start, end C.int) {
	a11y.mu.Lock()
	text := a11y.texts[uint64(id)].text
	a11y.mu.Unlock()
	s, e := runeOffset(text, int(start)), runeOffset(text, int(end))
	a11yCaret(uint64(id), func(c fyne.AccessibleTextCaret) { c.AccessibilitySetSelection(s, e) })
}

//export fyneA11ySetValue
func fyneA11ySetValue(id C.ulonglong, value *C.char) {
	v := C.GoString(value)
	obj, _ := a11yObject(uint64(id))
	if s, ok := obj.(fyne.AccessibleValueSetter); ok {
		fyne.Do(func() { s.AccessibilitySetValue(v) })
	}
}
