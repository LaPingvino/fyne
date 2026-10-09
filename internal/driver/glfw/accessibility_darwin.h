//go:build accessibility && darwin

#ifndef ACCESSIBILITY_DARWIN_H
#define ACCESSIBILITY_DARWIN_H

// The macOS bridge: NSAccessibility elements for a snapshot of each
// window's accessible objects, given by Go (accessibility_snapshot.go) on
// the main thread. An object keeps its element from one snapshot to the
// next. Text offsets are in UTF-16 code units, as NSString counts them.

enum {
    MacA11yFocusable = 1 << 0,
    MacA11yDisabled  = 1 << 1,
    MacA11yEditable  = 1 << 2,
    MacA11yMultiLine = 1 << 3,
    MacA11yPress     = 1 << 4,
    MacA11yChecked   = 1 << 5,
    MacA11ySelected  = 1 << 6,
    MacA11yExpanded  = 1 << 7,
    MacA11yIncrement = 1 << 8,
    MacA11yDecrement = 1 << 9,
    MacA11yShowMenu  = 1 << 10,
    MacA11ySetValue  = 1 << 11,
};

// A snapshot: Begin, a Node for every object of every window, End.
// nsWindow is the window the object is in; role is Fyne's (or "window").
void MacA11yBegin(void);
void MacA11yNode(unsigned long long id, unsigned long long parent, unsigned long long nsWindow,
    const char* role, const char* name, double x, double y, double width, double height, int flags,
    const unsigned long long* children, int childCount,
    const char* text, int caret, int selStart, int selEnd, const int* lines, int lineCount);
void MacA11yEnd(void);

// Events, after a snapshot.
void MacA11yFocus(unsigned long long id);
void MacA11yTextChanged(unsigned long long id);
void MacA11ySelectionChanged(unsigned long long id);

// A window is gone.
void MacA11yForgetWindow(unsigned long long nsWindow);

// For tests: the element of an object (NULL if there is none).
void* MacA11yElement(unsigned long long id);

#endif
