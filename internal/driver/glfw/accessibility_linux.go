//go:build accessibility && linux

package glfw

import (
	"github.com/LaPingvino/atspi"

	"fyne.io/fyne/v2"
)

// The Linux bridge: the snapshots (accessibility_snapshot.go) on the
// AT-SPI2 bus, for screen readers such as Orca.

// a11yConnect joins the accessibility bus; nil if there is none.
func a11yConnect(name string) a11yPlatform {
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
		return nil
	}
	return atspiPlatform{b}
}

// atspiPlatform is the AT-SPI bridge as an a11yPlatform.
type atspiPlatform struct{ *atspi.Bridge }

func (p atspiPlatform) Update(nodes []a11yNode) {
	out := make([]atspi.Node, len(nodes))
	for i, n := range nodes {
		out[i] = toAtspi(n)
	}
	p.Bridge.Update(out)
}

// toAtspi is a snapshot's object as AT-SPI describes it.
func toAtspi(n a11yNode) atspi.Node {
	a := atspi.Node{ID: n.ID, Parent: n.Parent, Children: n.Children, Name: n.Name, Actions: n.Actions,
		Bounds: atspi.Rect{X: n.Bounds.X, Y: n.Bounds.Y, Width: n.Bounds.Width, Height: n.Bounds.Height},
		States: []atspi.State{atspi.StateShowing, atspi.StateVisible}}
	if n.Window {
		a.Role = atspi.RoleFrame
		a.States = append(a.States, atspi.StateEnabled, atspi.StateSensitive)
		if n.Active {
			a.States = append(a.States, atspi.StateActive)
		}
		return a
	}
	a.Role = roleToAtspi(n.Role)
	for _, st := range n.States {
		switch st {
		case fyne.AccessibleStateChecked:
			a.States = append(a.States, atspi.StateChecked)
		case fyne.AccessibleStateExpanded:
			a.States = append(a.States, atspi.StateExpanded)
		case fyne.AccessibleStateInvalid:
			a.States = append(a.States, atspi.StateInvalidEntry)
		case fyne.AccessibleStateRequired:
			a.States = append(a.States, atspi.StateRequired)
		case fyne.AccessibleStateSelected:
			a.States = append(a.States, atspi.StateSelected)
		}
	}
	if !n.Disabled {
		a.States = append(a.States, atspi.StateEnabled, atspi.StateSensitive)
		if n.Focusable {
			a.States = append(a.States, atspi.StateFocusable)
		}
	}
	switch n.Role {
	case fyne.AccessibleRoleCheckbox, fyne.AccessibleRoleRadio:
		a.States = append(a.States, atspi.StateCheckable)
	case fyne.AccessibleRoleListItem, fyne.AccessibleRoleTreeItem, fyne.AccessibleRoleTab:
		a.States = append(a.States, atspi.StateSelectable)
	case fyne.AccessibleRoleTextField, fyne.AccessibleRoleTextArea:
		if !n.Disabled {
			a.States = append(a.States, atspi.StateEditable)
		}
		if n.Role == fyne.AccessibleRoleTextArea {
			a.States = append(a.States, atspi.StateMultiLine)
		} else {
			a.States = append(a.States, atspi.StateSingleLine)
		}
	}
	if n.Text != nil {
		a.Text = &atspi.Text{Content: n.Text.Content, Caret: n.Text.Caret,
			SelectionStart: n.Text.SelectionStart, SelectionEnd: n.Text.SelectionEnd, Lines: n.Text.Lines}
	}
	return a
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
