package container

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"
)

// The tab bar is one stop in the Tab order (the chosen tab); the arrow
// keys, Home and End choose the other tabs and focus them, skipping a
// disabled one; Tab goes on to the page.
func TestAppTabs_Keyboard(t *testing.T) {
	test.NewTempApp(t)
	pages := []*widget.Entry{widget.NewEntry(), widget.NewEntry(), widget.NewEntry(), widget.NewEntry()}
	tabs := NewAppTabs(
		NewTabItem("General", pages[0]),
		NewTabItem("Editor", pages[1]),
		NewTabItem("Hidden", pages[2]),
		NewTabItem("Script", pages[3]),
	)
	tabs.DisableIndex(2)
	w := test.NewTempWindow(t, tabs)
	w.Resize(fyne.NewSize(500, 300))
	c := w.Canvas()

	c.FocusNext()
	first, ok := c.Focused().(*tabButton)
	assert.True(t, ok, "Tab reaches the tab bar first")
	assert.Equal(t, tabs.Items[0].button, first)
	c.FocusNext()
	assert.Equal(t, pages[0], c.Focused(), "then the page, not the other tabs")

	c.Focus(first)
	first.TypedKey(&fyne.KeyEvent{Name: fyne.KeyRight})
	assert.Equal(t, 1, tabs.SelectedIndex(), "Right chooses the next tab")
	assert.Equal(t, tabs.Items[1].button, c.Focused(), "and focuses it")

	tabs.Items[1].button.TypedKey(&fyne.KeyEvent{Name: fyne.KeyRight})
	assert.Equal(t, 3, tabs.SelectedIndex(), "past the disabled tab")

	tabs.Items[3].button.TypedKey(&fyne.KeyEvent{Name: fyne.KeyRight})
	assert.Equal(t, 0, tabs.SelectedIndex(), "wrapping around")

	tabs.Items[0].button.TypedKey(&fyne.KeyEvent{Name: fyne.KeyEnd})
	assert.Equal(t, 3, tabs.SelectedIndex(), "End: the last")
	tabs.Items[3].button.TypedKey(&fyne.KeyEvent{Name: fyne.KeyHome})
	assert.Equal(t, 0, tabs.SelectedIndex(), "Home: the first")

	c.Unfocus()
	tabs.Select(tabs.Items[1])
	c.FocusNext()
	assert.Equal(t, tabs.Items[1].button, c.Focused(), "the stop is the chosen tab")
}
