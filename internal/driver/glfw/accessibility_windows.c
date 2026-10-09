//go:build accessibility && windows

// UI Automation providers for Fyne's accessible objects: the API of NVDA,
// JAWS, Narrator and braille displays. Go gives a snapshot of every
// window's objects on the main thread (WinA11yBegin, WinA11yNode,
// WinA11yEnd); an object keeps its element, and its runtime ID, from one
// snapshot to the next. Providers answer from the snapshot under a lock,
// on whatever thread UI Automation calls them, and never wait for Go;
// requests that change something (Invoke, SetFocus, Select, SetValue) are
// handed to Go, which does them on the main thread.
//
// Types, interface IDs and constants come from the Windows SDK headers
// (MinGW's); the UI Automation functions are loaded at run time.

#define CINTERFACE
#define COBJMACROS
#include <windows.h>
#include <ole2.h>
#include <oleauto.h>
#include <stddef.h>
#include <stdint.h>
#include <stdlib.h>
#include <string.h>
#include <wctype.h>
#include <initguid.h> // the UI Automation interface IDs, defined here
#include <uiautomation.h>
#include "accessibility_windows.h"

// Go, for the requests of assistive technologies (accessibility_windows.go).
extern void fyneA11yInvoke(unsigned long long id);
extern void fyneA11yFocus(unsigned long long id);
extern void fyneA11ySelect(unsigned long long id, int start, int end);
extern void fyneA11ySetValue(unsigned long long id, char* value);

#ifndef UiaRootObjectId
#define UiaRootObjectId (-25)
#endif
#ifndef UiaAppendRuntimeId
#define UiaAppendRuntimeId 3
#endif

// ---- UI Automation functions, loaded at run time ----

typedef LRESULT (WINAPI *PFN_UiaReturnRawElementProvider)(HWND, WPARAM, LPARAM, IRawElementProviderSimple*);
typedef HRESULT (WINAPI *PFN_UiaHostProviderFromHwnd)(HWND, IRawElementProviderSimple**);
typedef HRESULT (WINAPI *PFN_UiaRaiseAutomationEvent)(IRawElementProviderSimple*, EVENTID);
typedef HRESULT (WINAPI *PFN_UiaRaiseStructureChangedEvent)(IRawElementProviderSimple*, enum StructureChangeType, int*, int);
typedef HRESULT (WINAPI *PFN_UiaDisconnectProvider)(IRawElementProviderSimple*);
typedef HRESULT (WINAPI *PFN_UiaGetReservedNotSupportedValue)(IUnknown**);

static PFN_UiaReturnRawElementProvider uiaReturn;
static PFN_UiaHostProviderFromHwnd uiaHost;
static PFN_UiaRaiseAutomationEvent uiaRaiseEvent;
static PFN_UiaRaiseStructureChangedEvent uiaRaiseStructure;
static PFN_UiaDisconnectProvider uiaDisconnect;
static PFN_UiaGetReservedNotSupportedValue uiaNotSupported;

// ---- the snapshot ----

typedef struct Elem Elem;
struct Elem {
    IRawElementProviderSimple simple; // each interface: a vtable pointer
    IRawElementProviderFragment fragment;
    IRawElementProviderFragmentRoot fragRoot;
    ITextProvider textProvider;
    IValueProvider valueProvider;
    IInvokeProvider invokeProvider;
    LONG ref;

    unsigned long long id, parent;
    HWND hwnd;
    int isRoot; // a window
    int controlType;
    int flags;
    WCHAR* name;
    int x, y, width, height; // in the window's client area
    unsigned long long* children;
    int childCount;

    int hasText;
    WCHAR* text; // UTF-16
    int textLen;
    int caret, selStart, selEnd;
    int* lines; // where the shown lines start (NULL: at line breaks)
    int lineCount;

    int seen; // in the snapshot being built
};

typedef struct {
    ITextRangeProvider iface;
    LONG ref;
    Elem* elem;
    int start, end;
} Range;

static CRITICAL_SECTION lock;
static Elem** elems;
static int elemCount, elemCap;
static unsigned long long focusedID;

typedef struct { HWND hwnd; WNDPROC orig; } Hook;
static Hook* hooks;
static int hookCount, hookCap;

static IRawElementProviderSimpleVtbl simpleVtbl;
static IRawElementProviderFragmentVtbl fragmentVtbl;
static IRawElementProviderFragmentRootVtbl fragRootVtbl;
static ITextProviderVtbl textVtbl;
static ITextRangeProviderVtbl rangeVtbl;
static IValueProviderVtbl valueVtbl;
static IInvokeProviderVtbl invokeVtbl;

// IUnknown's ID, here (not in the UI Automation headers, and in a library
// of its own otherwise)
static const IID unknownIID = {0x00000000, 0x0000, 0x0000, {0xC0, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x46}};

#define FROM(type, member, p) ((Elem*)((char*)(p) - offsetof(Elem, member)))

static WCHAR* wide(const char* utf8) {
    int n = (utf8 && utf8[0]) ? MultiByteToWideChar(CP_UTF8, 0, utf8, -1, NULL, 0) : 1;
    WCHAR* w = (WCHAR*)calloc(n, sizeof(WCHAR));
    if (w && utf8 && utf8[0]) MultiByteToWideChar(CP_UTF8, 0, utf8, -1, w, n);
    return w;
}

static char* utf8(const WCHAR* w) {
    int n = WideCharToMultiByte(CP_UTF8, 0, w ? w : L"", -1, NULL, 0, NULL, NULL);
    char* s = (char*)calloc(n, 1);
    if (s) WideCharToMultiByte(CP_UTF8, 0, w ? w : L"", -1, s, n, NULL, NULL);
    return s;
}

static Elem* find(unsigned long long id) { // lock held
    for (int i = 0; i < elemCount; i++)
        if (elems[i]->id == id) return elems[i];
    return NULL;
}

static Elem* rootOf(HWND hwnd) { // lock held
    for (int i = 0; i < elemCount; i++)
        if (elems[i]->isRoot && elems[i]->hwnd == hwnd) return elems[i];
    return NULL;
}

static void freeElem(Elem* e) {
    free(e->name);
    free(e->children);
    free(e->text);
    free(e->lines);
    free(e);
}

static ULONG addRef(Elem* e) { return InterlockedIncrement(&e->ref); }
static ULONG release(Elem* e) {
    ULONG c = InterlockedDecrement(&e->ref);
    if (c == 0) freeElem(e);
    return c;
}

static int controlTypeOf(const char* role, int flags) {
    if (!strcmp(role, "window")) return UIA_WindowControlTypeId;
    if (!strcmp(role, "button")) return UIA_ButtonControlTypeId;
    if (!strcmp(role, "checkbox")) return UIA_CheckBoxControlTypeId;
    if (!strcmp(role, "heading")) return UIA_TextControlTypeId;
    if (!strcmp(role, "image")) return UIA_ImageControlTypeId;
    if (!strcmp(role, "link")) return UIA_HyperlinkControlTypeId;
    if (!strcmp(role, "list")) return UIA_ListControlTypeId;
    if (!strcmp(role, "listItem")) return UIA_ListItemControlTypeId;
    if (!strcmp(role, "progressBar")) return UIA_ProgressBarControlTypeId;
    if (!strcmp(role, "radio")) return UIA_RadioButtonControlTypeId;
    if (!strcmp(role, "separator")) return UIA_SeparatorControlTypeId;
    if (!strcmp(role, "slider")) return UIA_SliderControlTypeId;
    if (!strcmp(role, "tab")) return UIA_TabItemControlTypeId;
    if (!strcmp(role, "tabList")) return UIA_TabControlTypeId;
    if (!strcmp(role, "table")) return UIA_DataGridControlTypeId;
    if (!strcmp(role, "text")) return UIA_TextControlTypeId;
    if (!strcmp(role, "textField") || !strcmp(role, "textArea")) return UIA_EditControlTypeId;
    if (!strcmp(role, "tree")) return UIA_TreeControlTypeId;
    if (!strcmp(role, "treeItem")) return UIA_TreeItemControlTypeId;
    return UIA_PaneControlTypeId;
}

// ---- text units, in UTF-16 code units ----

static int isWordChar(WCHAR c) { return iswalnum(c) || c == L'\'' || c == 0x2019; }

// starts are the offsets at which units start, 0 and the end included;
// *n is how many. Lock held. The caller frees them.
static int* starts(Elem* e, enum TextUnit unit, int* n) {
    int len = e->textLen;
    int* b = (int*)malloc(sizeof(int) * (len + 2));
    int k = 0;
    if (!b) { *n = 0; return NULL; }
    b[k++] = 0;
    for (int i = 1; i < len; i++) {
        WCHAR c = e->text[i], p = e->text[i - 1];
        int start = 0;
        switch (unit) {
        case TextUnit_Character:
            start = !(c >= 0xDC00 && c <= 0xDFFF); // not the second half of a pair
            break;
        case TextUnit_Word:
            start = isWordChar(c) && !isWordChar(p);
            break;
        case TextUnit_Line:
            if (e->lines) {
                for (int l = 0; l < e->lineCount; l++)
                    if (e->lines[l] == i) start = 1;
            } else {
                start = p == L'\n';
            }
            break;
        case TextUnit_Paragraph:
            start = p == L'\n';
            break;
        default: // Format, Page, Document: the whole text
            break;
        }
        if (start) b[k++] = i;
    }
    if (len > 0 || k == 0) b[k++] = len;
    *n = k;
    return b;
}

// unitAt is the unit [*s, *e) around offset (lock held).
static void unitAt(Elem* e, enum TextUnit unit, int offset, int* s, int* t) {
    int n;
    int* b = starts(e, unit, &n);
    *s = 0;
    *t = e->textLen;
    if (!b) return;
    for (int i = 0; i + 1 < n; i++) {
        if (b[i] <= offset && (offset < b[i + 1] || i + 2 == n)) {
            *s = b[i];
            *t = b[i + 1];
            break;
        }
    }
    free(b);
}

// step moves offset count units (lock held); *moved is how many it did.
static int step(Elem* e, enum TextUnit unit, int offset, int count, int* moved) {
    int n;
    int* b = starts(e, unit, &n);
    *moved = 0;
    if (!b) return offset;
    while (count > 0) {
        int next = -1;
        for (int i = 0; i < n; i++)
            if (b[i] > offset) { next = b[i]; break; }
        if (next < 0) break;
        offset = next;
        count--;
        (*moved)++;
    }
    while (count < 0) {
        int prev = -1;
        for (int i = n - 1; i >= 0; i--)
            if (b[i] < offset) { prev = b[i]; break; }
        if (prev < 0) break;
        offset = prev;
        count++;
        (*moved)--;
    }
    free(b);
    return offset;
}

// ---- text ranges ----

static ITextRangeProvider* newRange(Elem* e, int start, int end) {
    Range* r = (Range*)calloc(1, sizeof(Range));
    if (!r) return NULL;
    r->iface.lpVtbl = &rangeVtbl;
    r->ref = 1;
    r->elem = e;
    addRef(e);
    r->start = start;
    r->end = end;
    return &r->iface;
}

#define RANGE(p) ((Range*)(p))

static void clampRange(Range* r) { // lock held
    int len = r->elem->textLen;
    if (r->start > len) r->start = len;
    if (r->end > len) r->end = len;
    if (r->start < 0) r->start = 0;
    if (r->end < r->start) r->end = r->start;
}

static HRESULT STDMETHODCALLTYPE R_QueryInterface(ITextRangeProvider* This, REFIID riid, void** ppv) {
    if (!ppv) return E_POINTER;
    if (IsEqualIID(riid, &unknownIID) || IsEqualIID(riid, &IID_ITextRangeProvider)) {
        *ppv = This;
        InterlockedIncrement(&RANGE(This)->ref);
        return S_OK;
    }
    *ppv = NULL;
    return E_NOINTERFACE;
}
static ULONG STDMETHODCALLTYPE R_AddRef(ITextRangeProvider* This) { return InterlockedIncrement(&RANGE(This)->ref); }
static ULONG STDMETHODCALLTYPE R_Release(ITextRangeProvider* This) {
    Range* r = RANGE(This);
    ULONG c = InterlockedDecrement(&r->ref);
    if (c == 0) {
        release(r->elem);
        free(r);
    }
    return c;
}
static HRESULT STDMETHODCALLTYPE R_Clone(ITextRangeProvider* This, ITextRangeProvider** out) {
    if (!out) return E_POINTER;
    *out = newRange(RANGE(This)->elem, RANGE(This)->start, RANGE(This)->end);
    return *out ? S_OK : E_OUTOFMEMORY;
}
static HRESULT STDMETHODCALLTYPE R_Compare(ITextRangeProvider* This, ITextRangeProvider* other, WINBOOL* out) {
    if (!out || !other) return E_POINTER;
    *out = other->lpVtbl == &rangeVtbl && RANGE(other)->elem == RANGE(This)->elem &&
        RANGE(other)->start == RANGE(This)->start && RANGE(other)->end == RANGE(This)->end;
    return S_OK;
}
static int endpoint(Range* r, enum TextPatternRangeEndpoint ep) {
    return ep == TextPatternRangeEndpoint_Start ? r->start : r->end;
}
static HRESULT STDMETHODCALLTYPE R_CompareEndpoints(ITextRangeProvider* This, enum TextPatternRangeEndpoint ep,
    ITextRangeProvider* target, enum TextPatternRangeEndpoint targetEp, int* out) {
    if (!out || !target) return E_POINTER;
    if (target->lpVtbl != &rangeVtbl) return E_INVALIDARG;
    *out = endpoint(RANGE(This), ep) - endpoint(RANGE(target), targetEp);
    return S_OK;
}
static HRESULT STDMETHODCALLTYPE R_ExpandToEnclosingUnit(ITextRangeProvider* This, enum TextUnit unit) {
    Range* r = RANGE(This);
    EnterCriticalSection(&lock);
    clampRange(r);
    unitAt(r->elem, unit, r->start, &r->start, &r->end);
    LeaveCriticalSection(&lock);
    return S_OK;
}
static HRESULT STDMETHODCALLTYPE R_FindAttribute(ITextRangeProvider* This, TEXTATTRIBUTEID id, VARIANT val,
    WINBOOL backward, ITextRangeProvider** out) {
    if (out) *out = NULL;
    return S_OK;
}
static HRESULT STDMETHODCALLTYPE R_FindText(ITextRangeProvider* This, BSTR text, WINBOOL backward,
    WINBOOL ignoreCase, ITextRangeProvider** out) {
    if (!out) return E_POINTER;
    *out = NULL;
    Range* r = RANGE(This);
    int n = text ? (int)wcslen(text) : 0;
    if (n == 0) return S_OK;
    EnterCriticalSection(&lock);
    clampRange(r);
    int found = -1;
    for (int i = backward ? r->end - n : r->start; backward ? i >= r->start : i + n <= r->end; i += backward ? -1 : 1) {
        int ok = 1;
        for (int k = 0; k < n && ok; k++) {
            WCHAR a = r->elem->text[i + k], b = text[k];
            ok = ignoreCase ? towlower(a) == towlower(b) : a == b;
        }
        if (ok) { found = i; break; }
    }
    LeaveCriticalSection(&lock);
    if (found >= 0) *out = newRange(r->elem, found, found + n);
    return S_OK;
}
static HRESULT STDMETHODCALLTYPE R_GetAttributeValue(ITextRangeProvider* This, TEXTATTRIBUTEID id, VARIANT* out) {
    if (!out) return E_POINTER;
    VariantInit(out);
    // not one this text has: the reserved value, which screen readers expect
    if (uiaNotSupported) {
        out->vt = VT_UNKNOWN;
        return uiaNotSupported(&out->punkVal);
    }
    return S_OK;
}
static HRESULT STDMETHODCALLTYPE R_GetBoundingRectangles(ITextRangeProvider* This, SAFEARRAY** out) {
    if (!out) return E_POINTER;
    *out = SafeArrayCreateVector(VT_R8, 0, 0);
    return S_OK;
}
static HRESULT STDMETHODCALLTYPE R_GetEnclosingElement(ITextRangeProvider* This, IRawElementProviderSimple** out) {
    if (!out) return E_POINTER;
    *out = &RANGE(This)->elem->simple;
    addRef(RANGE(This)->elem);
    return S_OK;
}
static HRESULT STDMETHODCALLTYPE R_GetText(ITextRangeProvider* This, int maxLength, BSTR* out) {
    if (!out) return E_POINTER;
    Range* r = RANGE(This);
    EnterCriticalSection(&lock);
    clampRange(r);
    int n = r->end - r->start;
    if (maxLength >= 0 && n > maxLength) n = maxLength;
    *out = SysAllocStringLen(r->elem->text ? r->elem->text + r->start : L"", n);
    LeaveCriticalSection(&lock);
    return *out ? S_OK : E_OUTOFMEMORY;
}
static HRESULT STDMETHODCALLTYPE R_Move(ITextRangeProvider* This, enum TextUnit unit, int count, int* moved) {
    if (!moved) return E_POINTER;
    Range* r = RANGE(This);
    EnterCriticalSection(&lock);
    clampRange(r);
    int degenerate = r->start == r->end;
    r->start = step(r->elem, unit, r->start, count, moved);
    if (degenerate) {
        r->end = r->start;
    } else {
        int s, e;
        unitAt(r->elem, unit, r->start, &s, &e);
        r->end = e;
    }
    LeaveCriticalSection(&lock);
    return S_OK;
}
static HRESULT STDMETHODCALLTYPE R_MoveEndpointByUnit(ITextRangeProvider* This, enum TextPatternRangeEndpoint ep,
    enum TextUnit unit, int count, int* moved) {
    if (!moved) return E_POINTER;
    Range* r = RANGE(This);
    EnterCriticalSection(&lock);
    clampRange(r);
    if (ep == TextPatternRangeEndpoint_Start) {
        r->start = step(r->elem, unit, r->start, count, moved);
        if (r->end < r->start) r->end = r->start;
    } else {
        r->end = step(r->elem, unit, r->end, count, moved);
        if (r->start > r->end) r->start = r->end;
    }
    LeaveCriticalSection(&lock);
    return S_OK;
}
static HRESULT STDMETHODCALLTYPE R_MoveEndpointByRange(ITextRangeProvider* This, enum TextPatternRangeEndpoint ep,
    ITextRangeProvider* target, enum TextPatternRangeEndpoint targetEp) {
    if (!target) return E_POINTER;
    if (target->lpVtbl != &rangeVtbl) return E_INVALIDARG;
    Range* r = RANGE(This);
    int at = endpoint(RANGE(target), targetEp);
    if (ep == TextPatternRangeEndpoint_Start) {
        r->start = at;
        if (r->end < at) r->end = at;
    } else {
        r->end = at;
        if (r->start > at) r->start = at;
    }
    return S_OK;
}
static HRESULT STDMETHODCALLTYPE R_Select(ITextRangeProvider* This) {
    fyneA11ySelect(RANGE(This)->elem->id, RANGE(This)->start, RANGE(This)->end);
    return S_OK;
}
static HRESULT STDMETHODCALLTYPE R_AddToSelection(ITextRangeProvider* This) { return R_Select(This); }
static HRESULT STDMETHODCALLTYPE R_RemoveFromSelection(ITextRangeProvider* This) { return S_OK; }
static HRESULT STDMETHODCALLTYPE R_ScrollIntoView(ITextRangeProvider* This, WINBOOL alignToTop) { return S_OK; }
static HRESULT STDMETHODCALLTYPE R_GetChildren(ITextRangeProvider* This, SAFEARRAY** out) {
    if (!out) return E_POINTER;
    *out = SafeArrayCreateVector(VT_UNKNOWN, 0, 0);
    return S_OK;
}

// ---- ITextProvider ----

static HRESULT STDMETHODCALLTYPE T_QueryInterface(ITextProvider* This, REFIID riid, void** ppv);
static ULONG STDMETHODCALLTYPE T_AddRef(ITextProvider* This) { return addRef(FROM(Elem, textProvider, This)); }
static ULONG STDMETHODCALLTYPE T_Release(ITextProvider* This) { return release(FROM(Elem, textProvider, This)); }

static SAFEARRAY* rangesOf(ITextRangeProvider* r) {
    SAFEARRAY* sa = SafeArrayCreateVector(VT_UNKNOWN, 0, r ? 1 : 0);
    if (sa && r) {
        LONG i = 0;
        SafeArrayPutElement(sa, &i, (IUnknown*)r); // the array keeps its own reference
        R_Release(r);
    }
    return sa;
}
static HRESULT STDMETHODCALLTYPE T_GetSelection(ITextProvider* This, SAFEARRAY** out) {
    if (!out) return E_POINTER;
    Elem* e = FROM(Elem, textProvider, This);
    EnterCriticalSection(&lock);
    int s = e->selStart, t = e->selEnd;
    if (s == t && e->caret >= 0) s = t = e->caret; // the caret: an empty range
    int any = e->caret >= 0 || s != t;
    LeaveCriticalSection(&lock);
    *out = rangesOf(any ? newRange(e, s, t) : NULL);
    return S_OK;
}
static HRESULT STDMETHODCALLTYPE T_GetVisibleRanges(ITextProvider* This, SAFEARRAY** out) {
    if (!out) return E_POINTER;
    Elem* e = FROM(Elem, textProvider, This);
    EnterCriticalSection(&lock);
    int len = e->textLen;
    LeaveCriticalSection(&lock);
    *out = rangesOf(newRange(e, 0, len));
    return S_OK;
}
static HRESULT STDMETHODCALLTYPE T_RangeFromChild(ITextProvider* This, IRawElementProviderSimple* child, ITextRangeProvider** out) {
    if (out) *out = NULL;
    return E_INVALIDARG;
}
static HRESULT STDMETHODCALLTYPE T_RangeFromPoint(ITextProvider* This, struct UiaPoint point, ITextRangeProvider** out) {
    if (!out) return E_POINTER;
    *out = newRange(FROM(Elem, textProvider, This), 0, 0);
    return S_OK;
}
static HRESULT STDMETHODCALLTYPE T_get_DocumentRange(ITextProvider* This, ITextRangeProvider** out) {
    if (!out) return E_POINTER;
    Elem* e = FROM(Elem, textProvider, This);
    EnterCriticalSection(&lock);
    int len = e->textLen;
    LeaveCriticalSection(&lock);
    *out = newRange(e, 0, len);
    return *out ? S_OK : E_OUTOFMEMORY;
}
static HRESULT STDMETHODCALLTYPE T_get_SupportedTextSelection(ITextProvider* This, enum SupportedTextSelection* out) {
    if (!out) return E_POINTER;
    *out = SupportedTextSelection_Single;
    return S_OK;
}

// ---- IValueProvider, IInvokeProvider ----

static HRESULT STDMETHODCALLTYPE V_QueryInterface(IValueProvider* This, REFIID riid, void** ppv);
static ULONG STDMETHODCALLTYPE V_AddRef(IValueProvider* This) { return addRef(FROM(Elem, valueProvider, This)); }
static ULONG STDMETHODCALLTYPE V_Release(IValueProvider* This) { return release(FROM(Elem, valueProvider, This)); }
static HRESULT STDMETHODCALLTYPE V_SetValue(IValueProvider* This, LPCWSTR val) {
    Elem* e = FROM(Elem, valueProvider, This);
    char* s = utf8(val);
    fyneA11ySetValue(e->id, s);
    free(s);
    return S_OK;
}
static HRESULT STDMETHODCALLTYPE V_get_Value(IValueProvider* This, BSTR* out) {
    if (!out) return E_POINTER;
    Elem* e = FROM(Elem, valueProvider, This);
    EnterCriticalSection(&lock);
    *out = SysAllocStringLen(e->text ? e->text : L"", e->textLen);
    LeaveCriticalSection(&lock);
    return *out ? S_OK : E_OUTOFMEMORY;
}
static HRESULT STDMETHODCALLTYPE V_get_IsReadOnly(IValueProvider* This, WINBOOL* out) {
    if (!out) return E_POINTER;
    Elem* e = FROM(Elem, valueProvider, This);
    *out = !(e->flags & WinA11yEditable) || (e->flags & WinA11yDisabled);
    return S_OK;
}

static HRESULT STDMETHODCALLTYPE I_QueryInterface(IInvokeProvider* This, REFIID riid, void** ppv);
static ULONG STDMETHODCALLTYPE I_AddRef(IInvokeProvider* This) { return addRef(FROM(Elem, invokeProvider, This)); }
static ULONG STDMETHODCALLTYPE I_Release(IInvokeProvider* This) { return release(FROM(Elem, invokeProvider, This)); }
static HRESULT STDMETHODCALLTYPE I_Invoke(IInvokeProvider* This) {
    fyneA11yInvoke(FROM(Elem, invokeProvider, This)->id);
    return S_OK;
}

// ---- an element's interfaces ----

static HRESULT query(Elem* e, REFIID riid, void** ppv) {
    if (!ppv) return E_POINTER;
    *ppv = NULL;
    if (IsEqualIID(riid, &unknownIID) || IsEqualIID(riid, &IID_IRawElementProviderSimple)) *ppv = &e->simple;
    else if (IsEqualIID(riid, &IID_IRawElementProviderFragment)) *ppv = &e->fragment;
    else if (IsEqualIID(riid, &IID_IRawElementProviderFragmentRoot) && e->isRoot) *ppv = &e->fragRoot;
    else if (IsEqualIID(riid, &IID_ITextProvider) && e->hasText) *ppv = &e->textProvider;
    else if (IsEqualIID(riid, &IID_IValueProvider) && e->hasText) *ppv = &e->valueProvider;
    else if (IsEqualIID(riid, &IID_IInvokeProvider) && (e->flags & WinA11yInvoke)) *ppv = &e->invokeProvider;
    if (!*ppv) return E_NOINTERFACE;
    addRef(e);
    return S_OK;
}
static HRESULT STDMETHODCALLTYPE T_QueryInterface(ITextProvider* This, REFIID riid, void** ppv) {
    return query(FROM(Elem, textProvider, This), riid, ppv);
}
static HRESULT STDMETHODCALLTYPE V_QueryInterface(IValueProvider* This, REFIID riid, void** ppv) {
    return query(FROM(Elem, valueProvider, This), riid, ppv);
}
static HRESULT STDMETHODCALLTYPE I_QueryInterface(IInvokeProvider* This, REFIID riid, void** ppv) {
    return query(FROM(Elem, invokeProvider, This), riid, ppv);
}

// IRawElementProviderSimple

static HRESULT STDMETHODCALLTYPE S_QueryInterface(IRawElementProviderSimple* This, REFIID riid, void** ppv) {
    return query(FROM(Elem, simple, This), riid, ppv);
}
static ULONG STDMETHODCALLTYPE S_AddRef(IRawElementProviderSimple* This) { return addRef(FROM(Elem, simple, This)); }
static ULONG STDMETHODCALLTYPE S_Release(IRawElementProviderSimple* This) { return release(FROM(Elem, simple, This)); }
static HRESULT STDMETHODCALLTYPE S_get_ProviderOptions(IRawElementProviderSimple* This, enum ProviderOptions* out) {
    if (!out) return E_POINTER;
    *out = ProviderOptions_ServerSideProvider | ProviderOptions_UseComThreading;
    return S_OK;
}
static HRESULT STDMETHODCALLTYPE S_GetPatternProvider(IRawElementProviderSimple* This, PATTERNID pattern, IUnknown** out) {
    if (!out) return E_POINTER;
    *out = NULL;
    Elem* e = FROM(Elem, simple, This);
    if (pattern == UIA_TextPatternId && e->hasText) *out = (IUnknown*)&e->textProvider;
    else if (pattern == UIA_ValuePatternId && e->hasText) *out = (IUnknown*)&e->valueProvider;
    else if (pattern == UIA_InvokePatternId && (e->flags & WinA11yInvoke)) *out = (IUnknown*)&e->invokeProvider;
    if (*out) addRef(e);
    return S_OK;
}
static void setBool(VARIANT* v, int b) {
    v->vt = VT_BOOL;
    v->boolVal = b ? VARIANT_TRUE : VARIANT_FALSE;
}
static HRESULT STDMETHODCALLTYPE S_GetPropertyValue(IRawElementProviderSimple* This, PROPERTYID pid, VARIANT* v) {
    if (!v) return E_POINTER;
    VariantInit(v);
    Elem* e = FROM(Elem, simple, This);
    EnterCriticalSection(&lock);
    int focused = focusedID != 0 && focusedID == e->id && GetForegroundWindow() == e->hwnd;
    switch (pid) {
    case UIA_ControlTypePropertyId:
        if (!e->isRoot) { v->vt = VT_I4; v->lVal = e->controlType; }
        break;
    case UIA_NamePropertyId:
        if (!e->isRoot && e->name) { v->vt = VT_BSTR; v->bstrVal = SysAllocString(e->name); }
        break;
    case UIA_AutomationIdPropertyId:
        if (!e->isRoot) {
            WCHAR buf[40];
            wsprintfW(buf, L"fyne_%I64u", e->id);
            v->vt = VT_BSTR;
            v->bstrVal = SysAllocString(buf);
        }
        break;
    case UIA_FrameworkIdPropertyId:
        v->vt = VT_BSTR;
        v->bstrVal = SysAllocString(L"Fyne");
        break;
    case UIA_HasKeyboardFocusPropertyId:
        if (!e->isRoot) setBool(v, focused);
        break;
    case UIA_IsKeyboardFocusablePropertyId:
        if (!e->isRoot) setBool(v, (e->flags & WinA11yFocusable) && !(e->flags & WinA11yDisabled));
        break;
    case UIA_IsEnabledPropertyId:
        setBool(v, !(e->flags & WinA11yDisabled));
        break;
    case UIA_IsControlElementPropertyId:
        setBool(v, 1);
        break;
    case UIA_IsContentElementPropertyId:
        setBool(v, e->controlType != UIA_SeparatorControlTypeId);
        break;
    case UIA_IsPasswordPropertyId:
        setBool(v, 0);
        break;
    case UIA_IsTextPatternAvailablePropertyId:
    case UIA_IsValuePatternAvailablePropertyId:
        setBool(v, e->hasText);
        break;
    case UIA_IsInvokePatternAvailablePropertyId:
        setBool(v, (e->flags & WinA11yInvoke) != 0);
        break;
    case UIA_ProviderDescriptionPropertyId:
        v->vt = VT_BSTR;
        v->bstrVal = SysAllocString(L"Fyne accessibility provider");
        break;
    }
    LeaveCriticalSection(&lock);
    return S_OK;
}
static HRESULT STDMETHODCALLTYPE S_get_HostRawElementProvider(IRawElementProviderSimple* This, IRawElementProviderSimple** out) {
    if (!out) return E_POINTER;
    *out = NULL;
    Elem* e = FROM(Elem, simple, This);
    if (e->isRoot && uiaHost) uiaHost(e->hwnd, out); // the window itself: its title, its frame
    return S_OK;
}

// IRawElementProviderFragment

static HRESULT STDMETHODCALLTYPE F_QueryInterface(IRawElementProviderFragment* This, REFIID riid, void** ppv) {
    return query(FROM(Elem, fragment, This), riid, ppv);
}
static ULONG STDMETHODCALLTYPE F_AddRef(IRawElementProviderFragment* This) { return addRef(FROM(Elem, fragment, This)); }
static ULONG STDMETHODCALLTYPE F_Release(IRawElementProviderFragment* This) { return release(FROM(Elem, fragment, This)); }

static int indexIn(Elem* parent, unsigned long long id) {
    for (int i = 0; parent && i < parent->childCount; i++)
        if (parent->children[i] == id) return i;
    return -1;
}
static HRESULT STDMETHODCALLTYPE F_Navigate(IRawElementProviderFragment* This, enum NavigateDirection dir,
    IRawElementProviderFragment** out) {
    if (!out) return E_POINTER;
    *out = NULL;
    Elem* e = FROM(Elem, fragment, This);
    Elem* t = NULL;
    EnterCriticalSection(&lock);
    Elem* parent = e->isRoot ? NULL : (e->parent ? find(e->parent) : rootOf(e->hwnd));
    int i;
    switch (dir) {
    case NavigateDirection_Parent:
        t = parent;
        break;
    case NavigateDirection_NextSibling:
        i = indexIn(parent, e->id);
        if (i >= 0 && i + 1 < parent->childCount) t = find(parent->children[i + 1]);
        break;
    case NavigateDirection_PreviousSibling:
        i = indexIn(parent, e->id);
        if (i > 0) t = find(parent->children[i - 1]);
        break;
    case NavigateDirection_FirstChild:
        if (e->childCount > 0) t = find(e->children[0]);
        break;
    case NavigateDirection_LastChild:
        if (e->childCount > 0) t = find(e->children[e->childCount - 1]);
        break;
    }
    if (t) {
        addRef(t);
        *out = &t->fragment;
    }
    LeaveCriticalSection(&lock);
    return S_OK;
}
static HRESULT STDMETHODCALLTYPE F_GetRuntimeId(IRawElementProviderFragment* This, SAFEARRAY** out) {
    if (!out) return E_POINTER;
    Elem* e = FROM(Elem, fragment, This);
    *out = NULL;
    if (e->isRoot) return S_OK; // the window's host provider has one
    int id[2] = {UiaAppendRuntimeId, (int)(e->id & 0x7fffffff)};
    SAFEARRAY* sa = SafeArrayCreateVector(VT_I4, 0, 2);
    if (!sa) return E_OUTOFMEMORY;
    for (LONG i = 0; i < 2; i++) SafeArrayPutElement(sa, &i, &id[i]);
    *out = sa;
    return S_OK;
}
static HRESULT STDMETHODCALLTYPE F_get_BoundingRectangle(IRawElementProviderFragment* This, struct UiaRect* out) {
    if (!out) return E_POINTER;
    Elem* e = FROM(Elem, fragment, This);
    EnterCriticalSection(&lock);
    POINT pt = {e->x, e->y};
    int w = e->width, h = e->height;
    HWND hwnd = e->hwnd;
    LeaveCriticalSection(&lock);
    ClientToScreen(hwnd, &pt);
    out->left = pt.x;
    out->top = pt.y;
    out->width = w;
    out->height = h;
    return S_OK;
}
static HRESULT STDMETHODCALLTYPE F_GetEmbeddedFragmentRoots(IRawElementProviderFragment* This, SAFEARRAY** out) {
    if (out) *out = NULL;
    return S_OK;
}
static HRESULT STDMETHODCALLTYPE F_SetFocus(IRawElementProviderFragment* This) {
    fyneA11yFocus(FROM(Elem, fragment, This)->id);
    return S_OK;
}
static HRESULT STDMETHODCALLTYPE F_get_FragmentRoot(IRawElementProviderFragment* This, IRawElementProviderFragmentRoot** out) {
    if (!out) return E_POINTER;
    *out = NULL;
    Elem* e = FROM(Elem, fragment, This);
    EnterCriticalSection(&lock);
    Elem* root = e->isRoot ? e : rootOf(e->hwnd);
    if (root) {
        addRef(root);
        *out = &root->fragRoot;
    }
    LeaveCriticalSection(&lock);
    return S_OK;
}

// IRawElementProviderFragmentRoot

static HRESULT STDMETHODCALLTYPE FR_QueryInterface(IRawElementProviderFragmentRoot* This, REFIID riid, void** ppv) {
    return query(FROM(Elem, fragRoot, This), riid, ppv);
}
static ULONG STDMETHODCALLTYPE FR_AddRef(IRawElementProviderFragmentRoot* This) { return addRef(FROM(Elem, fragRoot, This)); }
static ULONG STDMETHODCALLTYPE FR_Release(IRawElementProviderFragmentRoot* This) { return release(FROM(Elem, fragRoot, This)); }

static Elem* deepestAt(Elem* e, int x, int y) { // lock held
    for (int i = 0; i < e->childCount; i++) {
        Elem* c = find(e->children[i]);
        if (c && x >= c->x && x < c->x + c->width && y >= c->y && y < c->y + c->height) {
            Elem* d = deepestAt(c, x, y);
            return d ? d : c;
        }
    }
    return NULL;
}
static HRESULT STDMETHODCALLTYPE FR_ElementProviderFromPoint(IRawElementProviderFragmentRoot* This, double x, double y,
    IRawElementProviderFragment** out) {
    if (!out) return E_POINTER;
    *out = NULL;
    Elem* root = FROM(Elem, fragRoot, This);
    POINT pt = {(LONG)x, (LONG)y};
    ScreenToClient(root->hwnd, &pt);
    EnterCriticalSection(&lock);
    Elem* t = deepestAt(root, pt.x, pt.y);
    if (t) {
        addRef(t);
        *out = &t->fragment;
    }
    LeaveCriticalSection(&lock);
    return S_OK;
}
static HRESULT STDMETHODCALLTYPE FR_GetFocus(IRawElementProviderFragmentRoot* This, IRawElementProviderFragment** out) {
    if (!out) return E_POINTER;
    *out = NULL;
    Elem* root = FROM(Elem, fragRoot, This);
    EnterCriticalSection(&lock);
    Elem* t = focusedID ? find(focusedID) : NULL;
    if (t && t->hwnd == root->hwnd && t != root) {
        addRef(t);
        *out = &t->fragment;
    }
    LeaveCriticalSection(&lock);
    return S_OK;
}

static void initVtbls(void) {
    simpleVtbl.QueryInterface = S_QueryInterface;
    simpleVtbl.AddRef = S_AddRef;
    simpleVtbl.Release = S_Release;
    simpleVtbl.get_ProviderOptions = S_get_ProviderOptions;
    simpleVtbl.GetPatternProvider = S_GetPatternProvider;
    simpleVtbl.GetPropertyValue = S_GetPropertyValue;
    simpleVtbl.get_HostRawElementProvider = S_get_HostRawElementProvider;

    fragmentVtbl.QueryInterface = F_QueryInterface;
    fragmentVtbl.AddRef = F_AddRef;
    fragmentVtbl.Release = F_Release;
    fragmentVtbl.Navigate = F_Navigate;
    fragmentVtbl.GetRuntimeId = F_GetRuntimeId;
    fragmentVtbl.get_BoundingRectangle = F_get_BoundingRectangle;
    fragmentVtbl.GetEmbeddedFragmentRoots = F_GetEmbeddedFragmentRoots;
    fragmentVtbl.SetFocus = F_SetFocus;
    fragmentVtbl.get_FragmentRoot = F_get_FragmentRoot;

    fragRootVtbl.QueryInterface = FR_QueryInterface;
    fragRootVtbl.AddRef = FR_AddRef;
    fragRootVtbl.Release = FR_Release;
    fragRootVtbl.ElementProviderFromPoint = FR_ElementProviderFromPoint;
    fragRootVtbl.GetFocus = FR_GetFocus;

    textVtbl.QueryInterface = T_QueryInterface;
    textVtbl.AddRef = T_AddRef;
    textVtbl.Release = T_Release;
    textVtbl.GetSelection = T_GetSelection;
    textVtbl.GetVisibleRanges = T_GetVisibleRanges;
    textVtbl.RangeFromChild = T_RangeFromChild;
    textVtbl.RangeFromPoint = T_RangeFromPoint;
    textVtbl.get_DocumentRange = T_get_DocumentRange;
    textVtbl.get_SupportedTextSelection = T_get_SupportedTextSelection;

    rangeVtbl.QueryInterface = R_QueryInterface;
    rangeVtbl.AddRef = R_AddRef;
    rangeVtbl.Release = R_Release;
    rangeVtbl.Clone = R_Clone;
    rangeVtbl.Compare = R_Compare;
    rangeVtbl.CompareEndpoints = R_CompareEndpoints;
    rangeVtbl.ExpandToEnclosingUnit = R_ExpandToEnclosingUnit;
    rangeVtbl.FindAttribute = R_FindAttribute;
    rangeVtbl.FindText = R_FindText;
    rangeVtbl.GetAttributeValue = R_GetAttributeValue;
    rangeVtbl.GetBoundingRectangles = R_GetBoundingRectangles;
    rangeVtbl.GetEnclosingElement = R_GetEnclosingElement;
    rangeVtbl.GetText = R_GetText;
    rangeVtbl.Move = R_Move;
    rangeVtbl.MoveEndpointByUnit = R_MoveEndpointByUnit;
    rangeVtbl.MoveEndpointByRange = R_MoveEndpointByRange;
    rangeVtbl.Select = R_Select;
    rangeVtbl.AddToSelection = R_AddToSelection;
    rangeVtbl.RemoveFromSelection = R_RemoveFromSelection;
    rangeVtbl.ScrollIntoView = R_ScrollIntoView;
    rangeVtbl.GetChildren = R_GetChildren;

    valueVtbl.QueryInterface = V_QueryInterface;
    valueVtbl.AddRef = V_AddRef;
    valueVtbl.Release = V_Release;
    valueVtbl.SetValue = V_SetValue;
    valueVtbl.get_Value = V_get_Value;
    valueVtbl.get_IsReadOnly = V_get_IsReadOnly;

    invokeVtbl.QueryInterface = I_QueryInterface;
    invokeVtbl.AddRef = I_AddRef;
    invokeVtbl.Release = I_Release;
    invokeVtbl.Invoke = I_Invoke;
}

// ---- windows: UI Automation asks a window for its root ----

static WNDPROC origProc(HWND hwnd) {
    for (int i = 0; i < hookCount; i++)
        if (hooks[i].hwnd == hwnd) return hooks[i].orig;
    return NULL;
}

static LRESULT CALLBACK wndProc(HWND hwnd, UINT msg, WPARAM wParam, LPARAM lParam) {
    if (msg == WM_GETOBJECT && (LONG)lParam == UiaRootObjectId && uiaReturn) {
        EnterCriticalSection(&lock);
        Elem* root = rootOf(hwnd);
        if (root) addRef(root);
        LeaveCriticalSection(&lock);
        if (root) {
            LRESULT r = uiaReturn(hwnd, wParam, lParam, &root->simple);
            release(root);
            return r;
        }
    }
    WNDPROC orig = origProc(hwnd);
    return orig ? CallWindowProcW(orig, hwnd, msg, wParam, lParam) : DefWindowProcW(hwnd, msg, wParam, lParam);
}

static void hook(HWND hwnd) { // lock held
    if (!hwnd || origProc(hwnd)) return;
    if (hookCount == hookCap) {
        int cap = hookCap ? hookCap * 2 : 4;
        Hook* h = (Hook*)realloc(hooks, cap * sizeof(Hook));
        if (!h) return;
        hooks = h;
        hookCap = cap;
    }
    hooks[hookCount].hwnd = hwnd;
    hooks[hookCount].orig = (WNDPROC)SetWindowLongPtrW(hwnd, GWLP_WNDPROC, (LONG_PTR)wndProc);
    hookCount++;
}

// ---- the API for Go ----

void WinA11yInit(void) {
    static int done;
    if (done) return;
    done = 1;
    InitializeCriticalSection(&lock);
    initVtbls();
    HMODULE m = LoadLibraryW(L"uiautomationcore.dll");
    if (!m) return;
    uiaReturn = (PFN_UiaReturnRawElementProvider)GetProcAddress(m, "UiaReturnRawElementProvider");
    uiaHost = (PFN_UiaHostProviderFromHwnd)GetProcAddress(m, "UiaHostProviderFromHwnd");
    uiaRaiseEvent = (PFN_UiaRaiseAutomationEvent)GetProcAddress(m, "UiaRaiseAutomationEvent");
    uiaRaiseStructure = (PFN_UiaRaiseStructureChangedEvent)GetProcAddress(m, "UiaRaiseStructureChangedEvent");
    uiaDisconnect = (PFN_UiaDisconnectProvider)GetProcAddress(m, "UiaDisconnectProvider");
    uiaNotSupported = (PFN_UiaGetReservedNotSupportedValue)GetProcAddress(m, "UiaGetReservedNotSupportedValue");
}

void WinA11yBegin(void) {
    EnterCriticalSection(&lock);
    for (int i = 0; i < elemCount; i++) elems[i]->seen = 0;
    LeaveCriticalSection(&lock);
}

void WinA11yNode(unsigned long long id, unsigned long long parent, unsigned long long hwnd,
    const char* role, const char* name, int x, int y, int width, int height, int flags,
    const unsigned long long* children, int childCount,
    const char* text, int caret, int selStart, int selEnd, const int* lines, int lineCount) {
    EnterCriticalSection(&lock);
    Elem* e = find(id);
    int isNew = e == NULL;
    if (isNew) {
        if (elemCount == elemCap) {
            int cap = elemCap ? elemCap * 2 : 64;
            Elem** a = (Elem**)realloc(elems, cap * sizeof(Elem*));
            if (!a) { LeaveCriticalSection(&lock); return; }
            elems = a;
            elemCap = cap;
        }
        e = (Elem*)calloc(1, sizeof(Elem));
        if (!e) { LeaveCriticalSection(&lock); return; }
        e->simple.lpVtbl = &simpleVtbl;
        e->fragment.lpVtbl = &fragmentVtbl;
        e->fragRoot.lpVtbl = &fragRootVtbl;
        e->textProvider.lpVtbl = &textVtbl;
        e->valueProvider.lpVtbl = &valueVtbl;
        e->invokeProvider.lpVtbl = &invokeVtbl;
        e->ref = 1; // the snapshot's
        e->id = id;
        elems[elemCount++] = e;
    }
    e->seen = 1;
    e->parent = parent;
    e->hwnd = (HWND)(uintptr_t)hwnd;
    e->isRoot = !strcmp(role, "window");
    e->controlType = controlTypeOf(role, flags);
    e->flags = flags;
    free(e->name);
    e->name = wide(name);
    e->x = x;
    e->y = y;
    e->width = width;
    e->height = height;

    int childrenChanged = isNew || childCount != e->childCount ||
        (childCount > 0 && memcmp(children, e->children, childCount * sizeof(unsigned long long)) != 0);
    if (childrenChanged) {
        free(e->children);
        e->children = NULL;
        if (childCount > 0) {
            e->children = (unsigned long long*)malloc(childCount * sizeof(unsigned long long));
            if (e->children) memcpy(e->children, children, childCount * sizeof(unsigned long long));
        }
        e->childCount = e->children ? childCount : 0;
    }

    e->hasText = text != NULL;
    free(e->text);
    e->text = wide(text);
    e->textLen = e->text ? (int)wcslen(e->text) : 0;
    e->caret = caret;
    e->selStart = selStart;
    e->selEnd = selEnd;
    free(e->lines);
    e->lines = NULL;
    e->lineCount = 0;
    if (lines && lineCount > 0) {
        e->lines = (int*)malloc(lineCount * sizeof(int));
        if (e->lines) {
            memcpy(e->lines, lines, lineCount * sizeof(int));
            e->lineCount = lineCount;
        }
    }
    if (e->isRoot) hook(e->hwnd);
    int raise = childrenChanged && !isNew;
    if (raise) addRef(e);
    LeaveCriticalSection(&lock);

    if (raise) { // a screen reader reads the children again
        if (uiaRaiseStructure) {
            int rid[2] = {UiaAppendRuntimeId, (int)(id & 0x7fffffff)};
            uiaRaiseStructure(&e->simple, StructureChangeType_ChildrenInvalidated, rid, 2);
        }
        release(e);
    }
}

void WinA11yEnd(void) {
    Elem* gone[256];
    int n;
    do { // objects no longer shown: disconnected, then let go
        n = 0;
        EnterCriticalSection(&lock);
        for (int i = 0; i < elemCount && n < 256;) {
            if (!elems[i]->seen) {
                gone[n++] = elems[i];
                elems[i] = elems[--elemCount];
            } else {
                i++;
            }
        }
        if (focusedID) {
            int still = 0;
            for (int i = 0; i < elemCount; i++)
                if (elems[i]->id == focusedID) still = 1;
            if (!still) focusedID = 0;
        }
        LeaveCriticalSection(&lock);
        for (int i = 0; i < n; i++) {
            if (uiaDisconnect) uiaDisconnect(&gone[i]->simple);
            release(gone[i]);
        }
    } while (n == 256);
}

static void raise(unsigned long long id, EVENTID event) {
    EnterCriticalSection(&lock);
    Elem* e = find(id);
    if (e) addRef(e);
    LeaveCriticalSection(&lock);
    if (!e) return;
    if (uiaRaiseEvent) uiaRaiseEvent(&e->simple, event);
    release(e);
}

void WinA11yFocus(unsigned long long id) {
    EnterCriticalSection(&lock);
    focusedID = id;
    LeaveCriticalSection(&lock);
    if (id) raise(id, UIA_AutomationFocusChangedEventId);
}

void WinA11yTextChanged(unsigned long long id) { raise(id, UIA_Text_TextChangedEventId); }

// (the caret moving too: braille follows it from this)
void WinA11ySelectionChanged(unsigned long long id) { raise(id, UIA_Text_TextSelectionChangedEventId); }

void WinA11yForgetWindow(unsigned long long handle) {
    HWND hwnd = (HWND)(uintptr_t)handle;
    EnterCriticalSection(&lock);
    for (int i = 0; i < hookCount; i++) {
        if (hooks[i].hwnd == hwnd) {
            SetWindowLongPtrW(hooks[i].hwnd, GWLP_WNDPROC, (LONG_PTR)hooks[i].orig);
            hooks[i] = hooks[--hookCount];
            break;
        }
    }
    LeaveCriticalSection(&lock);
}
