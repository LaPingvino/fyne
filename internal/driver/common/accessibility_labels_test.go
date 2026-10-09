package common

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"
)

// Fields take the label beside them, or above them; a field with a name
// of its own (a button) keeps it.
func TestAutomaticLabels(t *testing.T) {
	test.NewTempApp(t)
	size := widget.NewEntry()
	size.SetPlaceHolder("12")
	sizeLabel := widget.NewLabel("Size:")
	row := container.NewBorder(nil, nil, sizeLabel, nil, size) // the label after the field in Objects
	notesLabel := widget.NewLabel("Notes")
	notes := widget.NewMultiLineEntry()
	stack := container.NewVBox(notesLabel, notes)
	ok := widget.NewButton("OK", nil)
	choice := widget.NewSelect([]string{"a", "b"}, nil)
	choiceLabel := widget.NewLabel("Theme:")
	choiceRow := container.NewHBox(choiceLabel, choice, ok)
	w := test.NewTempWindow(t, container.NewVBox(row, stack, choiceRow))
	w.Resize(fyne.NewSize(400, 400))

	assert.Equal(t, map[fyne.CanvasObject]string{size: "Size"}, AutomaticLabels(row.Objects))
	assert.Equal(t, map[fyne.CanvasObject]string{notes: "Notes"}, AutomaticLabels(stack.Objects))
	assert.Equal(t, map[fyne.CanvasObject]string{choice: "Theme"}, AutomaticLabels(choiceRow.Objects), "OK keeps its own name")

	fyne.SetAccessibleLabel(size, "Font size")
	defer fyne.SetAccessibleLabel(size, "")
	assert.Empty(t, AutomaticLabels(row.Objects), "a label the application set wins")
}
