package common

import (
	"strings"

	"fyne.io/fyne/v2"
)

// AutomaticLabels are names for the fields among siblings (the objects of
// one container), guessed from the layout for the automatic mode
// (fyne.AccessibilityAutomatic): a field takes the text of the label just
// left of it on the same row, or else just above it ("Size:" -> "Size").
// Text fields take such a label over their own (a placeholder); buttons
// and checkboxes only when they have none.
func AutomaticLabels(siblings []fyne.CanvasObject) map[fyne.CanvasObject]string {
	var labels, fields []fyne.CanvasObject
	for _, o := range siblings {
		a, ok := o.(fyne.Accessible)
		if !ok || !o.Visible() {
			continue
		}
		switch a.AccessibilityRole() {
		case fyne.AccessibleRoleText, fyne.AccessibleRoleHeading:
			if strings.TrimSpace(a.AccessibilityLabel()) != "" {
				labels = append(labels, o)
			}
		default:
			if _, focusable := o.(fyne.Focusable); focusable && !fyne.HasAccessibleLabel(o) && wantsLabel(a) {
				fields = append(fields, o)
			}
		}
	}
	out := map[fyne.CanvasObject]string{}
	for _, f := range fields {
		if l := nearestLabel(f, labels); l != nil {
			out[f] = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(fyne.AccessibleLabel(l)), ":"))
		}
	}
	return out
}

// wantsLabel reports whether a field would rather be named by a label
// beside it.
func wantsLabel(a fyne.Accessible) bool {
	switch a.AccessibilityRole() {
	case fyne.AccessibleRoleTextField, fyne.AccessibleRoleTextArea, fyne.AccessibleRoleSlider, fyne.AccessibleRoleProgressBar:
		return true
	}
	l := strings.TrimSpace(a.AccessibilityLabel())
	return l == "" || strings.HasPrefix(l, "(")
}

// nearestLabel is the label just left of f on its row, or else just above
// it; nil if none is close.
func nearestLabel(f fyne.CanvasObject, labels []fyne.CanvasObject) fyne.CanvasObject {
	fp, fs := f.Position(), f.Size()
	var best fyne.CanvasObject
	bestGap := float32(-1)
	for _, l := range labels {
		lp, ls := l.Position(), l.Size()
		sameRow := lp.Y < fp.Y+fs.Height && fp.Y < lp.Y+ls.Height
		var gap float32
		switch {
		case sameRow && lp.X+ls.Width <= fp.X+1:
			gap = fp.X - (lp.X + ls.Width)
		case !sameRow && lp.Y+ls.Height <= fp.Y+1 && lp.X < fp.X+fs.Width && fp.X < lp.X+ls.Width:
			gap = (fp.Y - (lp.Y + ls.Height)) * 2 // a label above counts as further than one beside
		default:
			continue
		}
		if gap < 0 {
			gap = 0
		}
		if gap > 200 {
			continue
		}
		if bestGap < 0 || gap < bestGap {
			best, bestGap = l, gap
		}
	}
	return best
}
