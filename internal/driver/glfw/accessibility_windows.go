//go:build accessibility && windows

package glfw

/*
#cgo LDFLAGS: -lole32 -loleaut32

#include <stdlib.h>
#include "accessibility_windows.h"
*/
import "C"

import (
	"slices"
	"unsafe"

	"fyne.io/fyne/v2"
)

// The Windows bridge: the snapshots (accessibility_snapshot.go) as UI
// Automation providers (accessibility_windows.c), for NVDA, JAWS, Narrator
// and braille displays. UI Automation counts text in UTF-16 code units;
// Fyne in runes: offsets are converted both ways here.

func a11yWindowHandle(w *window) uintptr {
	return uintptr(unsafe.Pointer(w.view().GetWin32Window()))
}

func a11yConnect(string) a11yPlatform {
	C.WinA11yInit()
	return winPlatform{}
}

type winPlatform struct{}

func (winPlatform) Update(nodes []a11yNode) {
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

	C.WinA11yBegin()
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
		C.WinA11yNode(C.ulonglong(n.ID), C.ulonglong(n.Parent), C.ulonglong(windowOf(n.ID)), cRole, cName,
			C.int(n.Bounds.X), C.int(n.Bounds.Y), C.int(n.Bounds.Width), C.int(n.Bounds.Height), C.int(winFlags(n)),
			children, C.int(len(n.Children)), cText, caret, start, end, cLines, C.int(len(lines)))
		C.free(unsafe.Pointer(cRole))
		C.free(unsafe.Pointer(cName))
		if cText != nil {
			C.free(unsafe.Pointer(cText))
		}
	}
	C.WinA11yEnd()
}

// winFlags are an object's states for the providers.
func winFlags(n a11yNode) int {
	f := 0
	set := func(on bool, flag int) {
		if on {
			f |= flag
		}
	}
	set(n.Focusable, C.WinA11yFocusable)
	set(n.Disabled, C.WinA11yDisabled)
	set((n.Role == fyne.AccessibleRoleTextField || n.Role == fyne.AccessibleRoleTextArea) && !n.Disabled, C.WinA11yEditable)
	set(n.Role == fyne.AccessibleRoleTextArea, C.WinA11yMultiLine)
	set(slices.Contains(n.Actions, string(fyne.AccessibleActionPress)), C.WinA11yInvoke)
	set(slices.Contains(n.States, fyne.AccessibleStateChecked), C.WinA11yChecked)
	set(slices.Contains(n.States, fyne.AccessibleStateSelected), C.WinA11ySelected)
	set(slices.Contains(n.States, fyne.AccessibleStateExpanded), C.WinA11yExpanded)
	return f
}

func (winPlatform) Focused(id uint64)                       { C.WinA11yFocus(C.ulonglong(id)) }
func (winPlatform) WindowActivated(uint64, bool)            {} // the window's own provider tells it
func (winPlatform) TextInserted(id uint64, _ int, _ string) { C.WinA11yTextChanged(C.ulonglong(id)) }
func (winPlatform) TextDeleted(id uint64, _ int, _ string)  { C.WinA11yTextChanged(C.ulonglong(id)) }

// CaretMoved: on Windows the caret is the (empty) selection; braille and
// screen readers follow it from TextSelectionChanged.
func (winPlatform) CaretMoved(id uint64, _ int) { C.WinA11ySelectionChanged(C.ulonglong(id)) }
func (winPlatform) SelectionChanged(id uint64)  { C.WinA11ySelectionChanged(C.ulonglong(id)) }
func (winPlatform) ForgetWindow(handle uintptr) { C.WinA11yForgetWindow(C.ulonglong(handle)) }

// The requests of assistive technologies, from UI Automation's threads.

//export fyneA11yInvoke
func fyneA11yInvoke(id C.ulonglong) {
	obj, _ := a11yObject(uint64(id))
	if a, ok := obj.(fyne.AccessibleActions); ok {
		fyne.Do(func() { a.AccessibilityPerformAction(fyne.AccessibleActionPress) })
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
