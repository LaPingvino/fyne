//go:build accessibility && (linux || windows || darwin)

package glfw

import (
	"sort"
	"sync"

	"github.com/go-gl/glfw/v3.4/glfw"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/internal/driver/common"
	"fyne.io/fyne/v2/internal/scale"
)

// Accessibility by snapshots, for the platforms whose bridge is written in
// this way: each repaint takes a snapshot of a window's accessible objects
// on the main thread (role, name, states, bounds, actions, text); the
// platform answers assistive technologies from the snapshots, and the
// changes they must be told of (the focus moving, a window becoming
// active, text typed, the caret moving) are found by comparing snapshots.
// Objects keep their IDs while they are shown, and an ID is not given
// again.

// a11yNode is an accessible object in a snapshot.
type a11yNode struct {
	ID, Parent uint64 // Parent 0: a window
	Children   []uint64
	Window     bool    // a window (its Role is unset)
	Handle     uintptr // a window's native handle (Windows)
	Active     bool    // the window has the focus
	Role       fyne.AccessibleRole
	Name       string
	Bounds     a11yRect // in pixels of the window
	Disabled   bool
	Focusable  bool
	States     []fyne.AccessibleState
	Actions    []string
	Text       *a11yText
}

type a11yRect struct{ X, Y, Width, Height int32 }

// a11yText is an object's text, in runes.
type a11yText struct {
	Content                      string
	Caret                        int // -1: no caret
	SelectionStart, SelectionEnd int
	Lines                        []int // where the shown lines start; nil: at line breaks
}

// a11yPlatform is a platform's bridge to its assistive technologies.
type a11yPlatform interface {
	textTeller
	Update(nodes []a11yNode) // all windows' objects
	Focused(id uint64)
	WindowActivated(id uint64, active bool)
	ForgetWindow(handle uintptr) // the window closes
}

// textTeller is what textEvents tells changes to.
type textTeller interface {
	TextInserted(id uint64, offset int, text string)
	TextDeleted(id uint64, offset int, text string)
	CaretMoved(id uint64, offset int)
	SelectionChanged(id uint64)
}

var a11y struct {
	once     sync.Once
	platform a11yPlatform // nil: no assistive technologies to tell

	mu      sync.Mutex
	next    uint64
	ids     map[fyne.CanvasObject]uint64 // an object keeps its ID while shown
	objects map[uint64]fyne.CanvasObject
	owner   map[uint64]*window
	windows map[*window]uint64
	nodes   map[*window][]a11yNode
	texts   map[uint64]textState
	active  *window
	focused uint64
}

// textState is an object's text when last seen.
type textState struct {
	text       string
	caret      int
	start, end int
}

// firstTitle is the title of the first window shown, the application's
// name for assistive technologies if it has none.
var firstTitle string

// a11yStart connects to the platform's assistive technologies, once.
func a11yStart() a11yPlatform {
	a11y.once.Do(func() {
		a11y.ids = map[fyne.CanvasObject]uint64{}
		a11y.objects = map[uint64]fyne.CanvasObject{}
		a11y.owner = map[uint64]*window{}
		a11y.windows = map[*window]uint64{}
		a11y.nodes = map[*window][]a11yNode{}
		a11y.texts = map[uint64]textState{}
		name := ""
		if app := fyne.CurrentApp(); app != nil {
			name = app.Metadata().Name
		}
		if name == "" {
			name = firstTitle // the application has no name of its own
		}
		a11y.platform = a11yConnect(name)
	})
	return a11y.platform
}

// idOf is obj's ID, given one the first time it is seen (a11y.mu held).
func idOf(obj fyne.CanvasObject) uint64 {
	if id, ok := a11y.ids[obj]; ok {
		return id
	}
	a11y.next++
	a11y.ids[obj] = a11y.next
	a11y.objects[a11y.next] = obj
	return a11y.next
}

func (w *window) updateAccessibility() {
	if w.view() == nil {
		return
	}
	if firstTitle == "" {
		firstTitle = w.title
	}
	p := a11yStart()
	if p == nil {
		return
	}

	a11y.mu.Lock()
	winID, ok := a11y.windows[w]
	if !ok {
		a11y.next++
		winID = a11y.next
		a11y.windows[w] = winID
	}
	size := w.canvas.Size()
	focusedWindow := w.view().GetAttrib(glfw.Focused) == glfw.True
	nodes := []a11yNode{{ID: winID, Window: true, Handle: a11yWindowHandle(w), Active: focusedWindow, Name: w.title,
		Bounds: a11yRect{Width: int32(scale.ToScreenCoordinate(w.canvas, size.Width)), Height: int32(scale.ToScreenCoordinate(w.canvas, size.Height))}}}
	seen := map[uint64]bool{}
	auto := fyne.AccessibilityAutomatic()
	guessed := map[fyne.CanvasObject]string{} // names the automatic mode found
	var walk func(obj fyne.CanvasObject, pos fyne.Position, parent int)
	walk = func(obj fyne.CanvasObject, pos fyne.Position, parent int) {
		if obj == nil || !obj.Visible() {
			return
		}
		objPos := pos.Add(obj.Position())
		if acc, ok := obj.(fyne.Accessible); ok && acc.AccessibilityRole() != fyne.AccessibleRoleContainer {
			id := idOf(obj)
			seen[id] = true
			a11y.owner[id] = w
			n := w.a11yNode(obj, acc, id, objPos)
			if name, ok := guessed[obj]; ok {
				n.Name = name
			}
			n.Parent = nodes[parent].ID
			nodes[parent].Children = append(nodes[parent].Children, id)
			nodes = append(nodes, n)
			parent = len(nodes) - 1
		}
		children := common.AccessibilityChildren(obj)
		if auto {
			for o, name := range common.AutomaticLabels(children) {
				guessed[o] = name
			}
		}
		for _, child := range children {
			walk(child, objPos, parent)
		}
	}
	if c := w.canvas.Content(); c != nil {
		walk(c, fyne.NewPos(0, 0), 0)
	}
	if w.canvas.menu != nil {
		walk(w.canvas.menu, fyne.NewPos(0, 0), 0)
	}
	for _, overlay := range w.canvas.Overlays().List() {
		walk(overlay, fyne.NewPos(0, 0), 0)
	}

	// objects of this window no longer shown are gone (their IDs are not
	// given again)
	for id, owner := range a11y.owner {
		if owner == w && !seen[id] {
			delete(a11y.ids, a11y.objects[id])
			delete(a11y.objects, id)
			delete(a11y.owner, id)
			delete(a11y.texts, id)
		}
	}
	sortByPosition(nodes)
	a11y.nodes[w] = nodes
	all := a11yAllNodes()

	// what changed, to be told after the update
	var events []func()
	for _, n := range nodes {
		if n.Text == nil {
			continue
		}
		now := textState{n.Text.Content, n.Text.Caret, n.Text.SelectionStart, n.Text.SelectionEnd}
		before, had := a11y.texts[n.ID]
		a11y.texts[n.ID] = now
		if !had {
			continue
		}
		events = append(events, textEvents(p, n.ID, before, now)...)
	}
	becameActive := focusedWindow && a11y.active != w
	if focusedWindow {
		a11y.active = w
	} else if a11y.active == w {
		a11y.active = nil
		events = append(events, func() { p.WindowActivated(winID, false) })
	}
	focused := uint64(0)
	if focusedWindow {
		if f, ok := w.canvas.Focused().(fyne.CanvasObject); ok {
			focused = a11y.ids[f]
		}
	}
	focusChanged := focusedWindow && focused != a11y.focused
	if focusChanged {
		a11y.focused = focused
	}
	a11y.mu.Unlock()

	p.Update(all)
	if becameActive {
		p.WindowActivated(winID, true)
	}
	if focusChanged {
		p.Focused(focused)
	}
	for _, e := range events {
		e()
	}
}

// a11yAllNodes are all windows' objects (a11y.mu held).
func a11yAllNodes() []a11yNode {
	var all []a11yNode
	for _, ns := range a11y.nodes {
		all = append(all, ns...)
	}
	return all
}

// a11yNode describes an accessible object.
func (w *window) a11yNode(obj fyne.CanvasObject, acc fyne.Accessible, id uint64, pos fyne.Position) a11yNode {
	px := func(v float32) int32 { return int32(scale.ToScreenCoordinate(w.canvas, v)) }
	n := a11yNode{ID: id, Role: acc.AccessibilityRole(), Name: fyne.AccessibleLabel(obj),
		Bounds: a11yRect{X: px(pos.X), Y: px(pos.Y), Width: px(obj.Size().Width), Height: px(obj.Size().Height)}}
	if s, ok := obj.(fyne.AccessibleStates); ok {
		for _, st := range s.AccessibilityStates() {
			if st == fyne.AccessibleStateDisabled {
				n.Disabled = true
				continue
			}
			n.States = append(n.States, st)
		}
	}
	if d, ok := obj.(fyne.Disableable); ok && d.Disabled() {
		n.Disabled = true
	}
	_, n.Focusable = obj.(fyne.Focusable)
	if a, ok := obj.(fyne.AccessibleActions); ok {
		for _, act := range a.AccessibilityActions() {
			n.Actions = append(n.Actions, string(act))
		}
	}
	if t, ok := obj.(fyne.AccessibleText); ok {
		start, end := t.AccessibilitySelection()
		n.Text = &a11yText{Content: t.AccessibilityText(), Caret: t.AccessibilityCaret(), SelectionStart: start, SelectionEnd: end}
		if l, ok := obj.(fyne.AccessibleTextLines); ok {
			n.Text.Lines = l.AccessibilityTextLines()
		}
	} else if v, ok := obj.(fyne.AccessibleValue); ok {
		n.Text = &a11yText{Content: v.AccessibilityValue(), Caret: -1}
	}
	return n
}

// textEvents are what a screen reader must hear of a text's change: the
// text inserted or deleted (the part that differs), then the caret.
func textEvents(b textTeller, id uint64, before, now textState) []func() {
	var events []func()
	if before.text != now.text {
		old, cur := []rune(before.text), []rune(now.text)
		p := 0
		for p < len(old) && p < len(cur) && old[p] == cur[p] {
			p++
		}
		s := 0
		for s < len(old)-p && s < len(cur)-p && old[len(old)-1-s] == cur[len(cur)-1-s] {
			s++
		}
		if deleted := string(old[p : len(old)-s]); deleted != "" {
			events = append(events, func() { b.TextDeleted(id, p, deleted) })
		}
		if inserted := string(cur[p : len(cur)-s]); inserted != "" {
			events = append(events, func() { b.TextInserted(id, p, inserted) })
		}
	}
	if before.caret != now.caret && now.caret >= 0 {
		events = append(events, func() { b.CaretMoved(id, now.caret) })
	}
	// (with nothing selected, start and end follow the caret: no change)
	if (before.start != before.end || now.start != now.end) && (before.start != now.start || before.end != now.end) {
		events = append(events, func() { b.SelectionChanged(id) })
	}
	return events
}

// sortByPosition puts each object's children in reading order: top to
// bottom, then left to right, as they are seen; the order of a layout's
// objects is not that (a Border's content comes before its top bar).
func sortByPosition(nodes []a11yNode) {
	at := make(map[uint64]a11yRect, len(nodes))
	for _, n := range nodes {
		at[n.ID] = n.Bounds
	}
	for i := range nodes {
		sort.SliceStable(nodes[i].Children, func(a, b int) bool {
			ra, rb := at[nodes[i].Children[a]], at[nodes[i].Children[b]]
			// on the same row: the one more to the left first
			if ra.Y+ra.Height <= rb.Y || rb.Y+rb.Height <= ra.Y {
				return ra.Y < rb.Y
			}
			return ra.X < rb.X
		})
	}
}

// The requests of assistive technologies, from the platform's threads:
// done on the main thread.

func a11yObject(id uint64) (fyne.CanvasObject, *window) {
	a11y.mu.Lock()
	defer a11y.mu.Unlock()
	return a11y.objects[id], a11y.owner[id]
}

func a11yDoAction(id uint64, index int) bool {
	obj, _ := a11yObject(id)
	a, ok := obj.(fyne.AccessibleActions)
	if !ok {
		return false
	}
	fyne.Do(func() {
		if acts := a.AccessibilityActions(); index < len(acts) {
			a.AccessibilityPerformAction(acts[index])
		}
	})
	return true
}

func a11yGrabFocus(id uint64) bool {
	obj, w := a11yObject(id)
	f, ok := obj.(fyne.Focusable)
	if !ok || w == nil {
		return false
	}
	fyne.Do(func() { w.canvas.Focus(f) })
	return true
}

func a11yCaret(id uint64, do func(fyne.AccessibleTextCaret)) bool {
	obj, _ := a11yObject(id)
	c, ok := obj.(fyne.AccessibleTextCaret)
	if !ok {
		return false
	}
	fyne.Do(func() { do(c) })
	return true
}

func (w *window) initAccessibilityForWindow() {}

func (w *window) cleanupAccessibilityForWindow() {
	if a11y.platform == nil {
		return
	}
	a11y.mu.Lock()
	delete(a11y.nodes, w)
	delete(a11y.windows, w)
	for id, owner := range a11y.owner {
		if owner == w {
			delete(a11y.ids, a11y.objects[id])
			delete(a11y.objects, id)
			delete(a11y.owner, id)
			delete(a11y.texts, id)
		}
	}
	if a11y.active == w {
		a11y.active = nil
	}
	all := a11yAllNodes()
	a11y.mu.Unlock()
	a11y.platform.Update(all)
	if w.view() != nil {
		a11y.platform.ForgetWindow(a11yWindowHandle(w))
	}
}

// utf16Offsets converts rune offsets in s to UTF-16 offsets.
func utf16Offsets(s string) func(int) int {
	at := make([]int, 0, len(s)+1)
	n := 0
	for _, r := range s {
		at = append(at, n)
		n += utf16Len(r)
	}
	at = append(at, n)
	return func(runes int) int { return at[min(max(runes, 0), len(at)-1)] }
}

// runeOffset converts a UTF-16 offset in s to runes.
func runeOffset(s string, units int) int {
	n, i := 0, 0
	for _, r := range s {
		if n >= units {
			return i
		}
		n += utf16Len(r)
		i++
	}
	return i
}

// utf16Len is how many UTF-16 code units r takes (two above the Basic
// Multilingual Plane).
func utf16Len(r rune) int {
	if r >= 0x10000 {
		return 2
	}
	return 1
}
