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
func (t *tellings) CaretMoved(id uint64, offset int) { *t = append(*t, fmt.Sprintf("caret %d", offset)) }
func (t *tellings) SelectionChanged(id uint64)       { *t = append(*t, "selection") }

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
