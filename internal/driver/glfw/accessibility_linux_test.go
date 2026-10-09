//go:build accessibility && linux

package glfw

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
)

type tellings []string

func (t *tellings) TextInserted(id uint64, offset int, s string) {
	*t = append(*t, fmt.Sprintf("insert %d %q", offset, s))
}
func (t *tellings) TextDeleted(id uint64, offset int, s string) {
	*t = append(*t, fmt.Sprintf("delete %d %q", offset, s))
}
func (t *tellings) CaretMoved(id uint64, offset int) {
	*t = append(*t, fmt.Sprintf("caret %d", offset))
}
func (t *tellings) SelectionChanged(id uint64) { *t = append(*t, "selection") }

// What a screen reader hears of text changing: the part that differs
// (in runes), then the caret; a selection only when there is one.
func TestTextEvents(t *testing.T) {
	for _, c := range []struct {
		before, now textState
		want        []string
	}{
		{textState{"Rain.", 5, 5, 5}, textState{"Rain.!", 6, 6, 6}, []string{`insert 5 "!"`, "caret 6"}},
		{textState{"Café au lait", 4, 4, 4}, textState{"Caf au lait", 3, 3, 3}, []string{`delete 3 "é"`, "caret 3"}},
		{textState{"one two", 4, 4, 4}, textState{"one 2", 5, 5, 5}, []string{`delete 4 "two"`, `insert 4 "2"`, "caret 5"}},
		{textState{"abc", 1, 1, 1}, textState{"abc", 2, 2, 2}, []string{"caret 2"}},
		{textState{"abc", 1, 1, 1}, textState{"abc", 3, 1, 3}, []string{"caret 3", "selection"}},
		{textState{"abc", -1, 0, 0}, textState{"abc", -1, 0, 0}, nil},
	} {
		var got tellings
		for _, e := range textEvents(&got, 1, c.before, c.now) {
			e()
		}
		assert.Equal(t, c.want, []string(got), "%v -> %v", c.before, c.now)
	}
}

// Children in reading order: rows top to bottom, each left to right.
func TestSortByPosition(t *testing.T) {
	nodes := []a11yNode{
		{ID: 1, Children: []uint64{2, 3, 4, 5}},
		{ID: 2, Bounds: a11yRect{X: 0, Y: 100, Width: 500, Height: 400}}, // the content
		{ID: 3, Bounds: a11yRect{X: 60, Y: 0, Width: 40, Height: 40}},    // toolbar, second button
		{ID: 4, Bounds: a11yRect{X: 0, Y: 500, Width: 500, Height: 20}},  // status bar
		{ID: 5, Bounds: a11yRect{X: 10, Y: 5, Width: 40, Height: 30}},    // toolbar, first button
	}
	sortByPosition(nodes)
	assert.Equal(t, []uint64{5, 3, 2, 4}, nodes[0].Children)
}
