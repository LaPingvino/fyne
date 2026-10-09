//go:build accessibility && linux

package glfw

import (
	"sort"
	"sync"

	"github.com/LaPingvino/atspi"
	"github.com/go-gl/glfw/v3.4/glfw"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/internal/driver/common"
	"fyne.io/fyne/v2/internal/scale"
)

// The Linux accessibility bridge: Fyne's accessible objects on the AT-SPI2
// bus, for screen readers such as Orca. Each repaint takes a snapshot of a
// window's objects on the main thread; the bridge answers assistive
// technologies from the snapshots on its own goroutines, and changes that
// a screen reader must hear (the focus moving, a window becoming active,
// text typed, the caret moving) are found by comparing snapshots.

var a11y struct {
	once   sync.Once
	bridge *atspi.Bridge // nil: no accessibility bus

	mu      sync.Mutex
	next    uint64
	ids     map[fyne.CanvasObject]uint64 // an object keeps its ID while shown
	objects map[uint64]fyne.CanvasObject
	owner   map[uint64]*window
	windows map[*window]uint64
	nodes   map[*window][]atspi.Node
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
// name for screen readers if it has none.
var firstTitle string

func a11yBridge() *atspi.Bridge {
	a11y.once.Do(func() {
		a11y.ids = map[fyne.CanvasObject]uint64{}
		a11y.objects = map[uint64]fyne.CanvasObject{}
		a11y.owner = map[uint64]*window{}
		a11y.windows = map[*window]uint64{}
		a11y.nodes = map[*window][]atspi.Node{}
		a11y.texts = map[uint64]textState{}
		name := ""
		if app := fyne.CurrentApp(); app != nil {
			name = app.Metadata().Name
		}
		if name == "" {
			name = firstTitle // the application has no name of its own
		}
		b, err := atspi.Connect(atspi.AppInfo{Name: name, ToolkitName: "Fyne", ToolkitVersion: "2"}, atspi.Handler{
			DoAction:  a11yDoAction,
			GrabFocus: a11yGrabFocus,
			SetCaret: func(id uint64, offset int) bool {
				return a11yCaret(id, func(c fyne.AccessibleTextCaret) { c.AccessibilitySetCaret(offset) })
			},
			SetSelection: func(id uint64, start, end int) bool {
				return a11yCaret(id, func(c fyne.AccessibleTextCaret) { c.AccessibilitySetSelection(start, end) })
			},
		})
		if err != nil {
			fyne.LogError("Accessibility bus not available", err)
			return
		}
		a11y.bridge = b
	})
	return a11y.bridge
}

// idOf is obj's ID, given one the first time it is seen (a.mu held).
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
	b := a11yBridge()
	if b == nil {
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
	win := atspi.Node{ID: winID, Role: atspi.RoleFrame, Name: w.title,
		States: []atspi.State{atspi.StateShowing, atspi.StateVisible, atspi.StateEnabled, atspi.StateSensitive},
		Bounds: atspi.Rect{Width: int32(scale.ToScreenCoordinate(w.canvas, size.Width)), Height: int32(scale.ToScreenCoordinate(w.canvas, size.Height))}}
	if focusedWindow {
		win.States = append(win.States, atspi.StateActive)
	}
	nodes := []atspi.Node{win}
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
	var all []atspi.Node
	for _, ns := range a11y.nodes {
		all = append(all, ns...)
	}

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
		id := n.ID
		events = append(events, textEvents(b, id, before, now)...)
	}
	becameActive := focusedWindow && a11y.active != w
	if focusedWindow {
		a11y.active = w
	} else if a11y.active == w {
		a11y.active = nil
		events = append(events, func() { b.WindowActivated(winID, false) })
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

	b.Update(all)
	if becameActive {
		b.WindowActivated(winID, true)
	}
	if focusChanged {
		b.Focused(focused)
	}
	for _, e := range events {
		e()
	}
}

// a11yNode describes an accessible object.
func (w *window) a11yNode(obj fyne.CanvasObject, acc fyne.Accessible, id uint64, pos fyne.Position) atspi.Node {
	role := acc.AccessibilityRole()
	px := func(v float32) int32 { return int32(scale.ToScreenCoordinate(w.canvas, v)) }
	n := atspi.Node{ID: id, Role: roleToAtspi(role), Name: fyne.AccessibleLabel(obj),
		Bounds: atspi.Rect{X: px(pos.X), Y: px(pos.Y), Width: px(obj.Size().Width), Height: px(obj.Size().Height)},
		States: []atspi.State{atspi.StateShowing, atspi.StateVisible}}

	disabled := false
	if s, ok := obj.(fyne.AccessibleStates); ok {
		for _, st := range s.AccessibilityStates() {
			switch st {
			case fyne.AccessibleStateDisabled:
				disabled = true
			case fyne.AccessibleStateChecked:
				n.States = append(n.States, atspi.StateChecked)
			case fyne.AccessibleStateExpanded:
				n.States = append(n.States, atspi.StateExpanded)
			case fyne.AccessibleStateInvalid:
				n.States = append(n.States, atspi.StateInvalidEntry)
			case fyne.AccessibleStateRequired:
				n.States = append(n.States, atspi.StateRequired)
			case fyne.AccessibleStateSelected:
				n.States = append(n.States, atspi.StateSelected)
			}
		}
	}
	if d, ok := obj.(fyne.Disableable); ok && d.Disabled() {
		disabled = true
	}
	if !disabled {
		n.States = append(n.States, atspi.StateEnabled, atspi.StateSensitive)
	}
	if _, ok := obj.(fyne.Focusable); ok && !disabled {
		n.States = append(n.States, atspi.StateFocusable)
	}
	switch role {
	case fyne.AccessibleRoleCheckbox, fyne.AccessibleRoleRadio:
		n.States = append(n.States, atspi.StateCheckable)
	case fyne.AccessibleRoleListItem, fyne.AccessibleRoleTreeItem, fyne.AccessibleRoleTab:
		n.States = append(n.States, atspi.StateSelectable)
	case fyne.AccessibleRoleTextField, fyne.AccessibleRoleTextArea:
		if !disabled {
			n.States = append(n.States, atspi.StateEditable)
		}
		if role == fyne.AccessibleRoleTextArea {
			n.States = append(n.States, atspi.StateMultiLine)
		} else {
			n.States = append(n.States, atspi.StateSingleLine)
		}
	}

	if a, ok := obj.(fyne.AccessibleActions); ok {
		for _, act := range a.AccessibilityActions() {
			n.Actions = append(n.Actions, string(act))
		}
	}
	if t, ok := obj.(fyne.AccessibleText); ok {
		start, end := t.AccessibilitySelection()
		n.Text = &atspi.Text{Content: t.AccessibilityText(), Caret: t.AccessibilityCaret(), SelectionStart: start, SelectionEnd: end}
		if l, ok := obj.(fyne.AccessibleTextLines); ok {
			n.Text.Lines = l.AccessibilityTextLines()
		}
	} else if v, ok := obj.(fyne.AccessibleValue); ok {
		n.Text = &atspi.Text{Content: v.AccessibilityValue(), Caret: -1}
	}
	return n
}

// textTeller is what textEvents tells changes to (the bridge).
type textTeller interface {
	TextInserted(id uint64, offset int, text string)
	TextDeleted(id uint64, offset int, text string)
	CaretMoved(id uint64, offset int)
	SelectionChanged(id uint64)
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

func roleToAtspi(role fyne.AccessibleRole) atspi.Role {
	switch role {
	case fyne.AccessibleRoleButton:
		return atspi.RoleButton
	case fyne.AccessibleRoleCheckbox:
		return atspi.RoleCheckBox
	case fyne.AccessibleRoleHeading:
		return atspi.RoleHeading
	case fyne.AccessibleRoleImage:
		return atspi.RoleImage
	case fyne.AccessibleRoleLink:
		return atspi.RoleLink
	case fyne.AccessibleRoleList:
		return atspi.RoleList
	case fyne.AccessibleRoleListItem:
		return atspi.RoleListItem
	case fyne.AccessibleRoleProgressBar:
		return atspi.RoleProgressBar
	case fyne.AccessibleRoleRadio:
		return atspi.RoleRadioButton
	case fyne.AccessibleRoleSeparator:
		return atspi.RoleSeparator
	case fyne.AccessibleRoleSlider:
		return atspi.RoleSlider
	case fyne.AccessibleRoleTab:
		return atspi.RolePageTab
	case fyne.AccessibleRoleTabList:
		return atspi.RolePageTabList
	case fyne.AccessibleRoleTable:
		return atspi.RoleTable
	case fyne.AccessibleRoleText:
		return atspi.RoleLabel
	case fyne.AccessibleRoleTextField:
		return atspi.RoleEntry
	case fyne.AccessibleRoleTextArea:
		return atspi.RoleText
	case fyne.AccessibleRoleTree:
		return atspi.RoleTree
	case fyne.AccessibleRoleTreeItem:
		return atspi.RoleTreeItem
	}
	return atspi.RolePanel
}

// The requests of assistive technologies, from the bridge's goroutines:
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
	if a11y.bridge == nil {
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
	var all []atspi.Node
	for _, ns := range a11y.nodes {
		all = append(all, ns...)
	}
	a11y.mu.Unlock()
	a11y.bridge.Update(all)
}

// sortByPosition puts each object's children in reading order: top to
// bottom, then left to right, as they are seen; the order of a layout's
// objects is not that (a Border's content comes before its top bar).
func sortByPosition(nodes []atspi.Node) {
	at := make(map[uint64]atspi.Rect, len(nodes))
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
