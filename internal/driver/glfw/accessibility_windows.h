//go:build accessibility && windows

#ifndef ACCESSIBILITY_WINDOWS_H
#define ACCESSIBILITY_WINDOWS_H

// The Windows bridge: UI Automation providers for a snapshot of each
// window's accessible objects, given by Go (accessibility_snapshot.go) on
// the main thread. Text offsets are in UTF-16 code units, as UI Automation
// counts them.

enum {
    WinA11yFocusable = 1 << 0,
    WinA11yDisabled  = 1 << 1,
    WinA11yEditable  = 1 << 2,
    WinA11yMultiLine = 1 << 3,
    WinA11yInvoke    = 1 << 4,
    WinA11yChecked   = 1 << 5,
    WinA11ySelected  = 1 << 6,
    WinA11yExpanded  = 1 << 7,
};

void WinA11yInit(void);

// A snapshot: Begin, a Node for every object of every window, End.
void WinA11yBegin(void);
void WinA11yNode(unsigned long long id, unsigned long long parent, unsigned long long hwnd,
    const char* role, const char* name, int x, int y, int width, int height, int flags,
    const unsigned long long* children, int childCount,
    const char* text, int caret, int selStart, int selEnd, const int* lines, int lineCount);
void WinA11yEnd(void);

// Events, after a snapshot.
void WinA11yFocus(unsigned long long id);
void WinA11yTextChanged(unsigned long long id);
void WinA11ySelectionChanged(unsigned long long id);

// A window is gone.
void WinA11yForgetWindow(unsigned long long hwnd);

#endif
