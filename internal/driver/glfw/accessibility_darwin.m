//go:build accessibility && darwin

// NSAccessibility elements for Fyne's accessible objects: what VoiceOver
// (and braille through it) reads. Go gives a snapshot of every window's
// objects on the main thread (MacA11yBegin, MacA11yNode, MacA11yEnd); an
// object keeps its element from one snapshot to the next, so VoiceOver can
// follow it (and its caret). AppKit asks on the main thread too, and is
// answered from the snapshot; requests that change something (press,
// focus, select, set value) are handed to Go.
//
// GLFW's content view hides from accessibility: methods added to its class
// (and its window's) make it the parent of the window's elements, and let
// it answer for the focus and for hit tests.

#import <Cocoa/Cocoa.h>
#import <objc/runtime.h>
#include <stdint.h>
#include "accessibility_darwin.h"

// Go, for the requests of assistive technologies (accessibility_darwin.go).
extern void fyneA11yAction(unsigned long long id, int action);
extern void fyneA11yFocus(unsigned long long id);
extern void fyneA11ySelect(unsigned long long id, int start, int end);
extern void fyneA11ySetValue(unsigned long long id, char* value);

enum { actionPress, actionIncrement, actionDecrement, actionShowMenu };

@class FyneA11yElement;

static NSMutableDictionary<NSNumber*, FyneA11yElement*>* elements; // by ID
static NSMutableDictionary<NSValue*, NSNumber*>* windowIDs;          // NSWindow -> its node's ID
static NSMutableDictionary<NSNumber*, NSArray<NSNumber*>*>* windowChildren; // window node -> top objects
static NSMutableSet<NSNumber*>* seen;
static unsigned long long focusedID;

static FyneA11yElement* elementFor(NSNumber* n) { return n ? elements[n] : nil; }

static NSArray* elementsFor(NSArray<NSNumber*>* ids) {
    NSMutableArray* out = [NSMutableArray arrayWithCapacity:[ids count]];
    for (NSNumber* n in ids) {
        FyneA11yElement* e = elementFor(n);
        if (e) [out addObject:e];
    }
    return out;
}

// the top objects of the window whose content view is view
static NSArray* topElements(NSView* view) {
    NSNumber* wid = windowIDs[[NSValue valueWithPointer:[view window]]];
    return wid ? elementsFor(windowChildren[wid]) : @[];
}

@interface FyneA11yElement : NSAccessibilityElement {
@public
    unsigned long long eid;
    unsigned long long parentID; // 0 or a window node: the content view
    NSWindow* window;            // not retained: the window outlives its elements
    NSString* role;
    NSString* label;
    NSString* text;              // nil: no text
    NSRect local;                // in the content view, top-left origin, points
    int flags;
    NSArray<NSNumber*>* childIDs;
    NSInteger caret, selStart, selEnd;
    NSArray<NSNumber*>* lines;   // where the shown lines start; nil: at line breaks
}
@end

@implementation FyneA11yElement

- (void)dealloc {
    [role release];
    [label release];
    [text release];
    [childIDs release];
    [lines release];
    [super dealloc];
}

- (NSAccessibilityRole)accessibilityRole {
    if ([role isEqualToString:@"button"]) return NSAccessibilityButtonRole;
    if ([role isEqualToString:@"checkbox"]) return NSAccessibilityCheckBoxRole;
    if ([role isEqualToString:@"heading"] || [role isEqualToString:@"text"]) return NSAccessibilityStaticTextRole;
    if ([role isEqualToString:@"image"]) return NSAccessibilityImageRole;
    if ([role isEqualToString:@"link"]) return NSAccessibilityLinkRole;
    if ([role isEqualToString:@"list"]) return NSAccessibilityListRole;
    if ([role isEqualToString:@"listItem"] || [role isEqualToString:@"treeItem"]) return NSAccessibilityRowRole;
    if ([role isEqualToString:@"progressBar"]) return NSAccessibilityProgressIndicatorRole;
    if ([role isEqualToString:@"radio"] || [role isEqualToString:@"tab"]) return NSAccessibilityRadioButtonRole;
    if ([role isEqualToString:@"separator"]) return @"AXSeparator";
    if ([role isEqualToString:@"slider"]) return NSAccessibilitySliderRole;
    if ([role isEqualToString:@"tabList"]) return NSAccessibilityTabGroupRole;
    if ([role isEqualToString:@"table"]) return NSAccessibilityTableRole;
    if ([role isEqualToString:@"textField"]) return NSAccessibilityTextFieldRole;
    if ([role isEqualToString:@"textArea"]) return NSAccessibilityTextAreaRole;
    if ([role isEqualToString:@"tree"]) return NSAccessibilityOutlineRole;
    return NSAccessibilityGroupRole;
}

- (NSAccessibilitySubrole)accessibilitySubrole {
    if ([role isEqualToString:@"tab"]) return NSAccessibilityTabButtonSubrole;
    return nil;
}

- (NSString*)accessibilityRoleDescription {
    if ([role isEqualToString:@"heading"]) return @"heading";
    return NSAccessibilityRoleDescription([self accessibilityRole], [self accessibilitySubrole]);
}

- (NSString*)accessibilityLabel { return label; }
- (NSString*)accessibilityTitle { return nil; } // the label says it (VoiceOver would say it twice)

- (id)accessibilityValue {
    if ([role isEqualToString:@"checkbox"]) return @((flags & MacA11yChecked) ? 1 : 0);
    if ([role isEqualToString:@"radio"] || [role isEqualToString:@"tab"]) return @((flags & MacA11ySelected) ? 1 : 0);
    return text;
}

- (void)setAccessibilityValue:(id)value {
    NSString* s = [value isKindOfClass:[NSString class]] ? value :
        ([value respondsToSelector:@selector(stringValue)] ? [value stringValue] : nil);
    if (s && (flags & MacA11ySetValue)) fyneA11ySetValue(eid, (char*)[s UTF8String]);
}

// screen points, bottom-left origin, from the content view's top-left ones
- (NSRect)frameOf:(NSRect)r {
    NSView* view = [window contentView];
    if (!view) return NSZeroRect;
    NSRect inWindow = [view convertRect:NSMakeRect(r.origin.x, [view bounds].size.height - r.origin.y - r.size.height,
                                                   r.size.width, r.size.height)
                                 toView:nil];
    return [window convertRectToScreen:inWindow];
}

- (NSRect)accessibilityFrame { return [self frameOf:local]; }

- (id)accessibilityParent {
    FyneA11yElement* p = elementFor(@(parentID));
    return p ? (id)p : (id)[window contentView];
}
- (id)accessibilityWindow { return window; }
- (id)accessibilityTopLevelUIElement { return window; }
- (NSArray*)accessibilityChildren { return elementsFor(childIDs); }

- (id)accessibilityHitTest:(NSPoint)point {
    if (!NSPointInRect(point, [self accessibilityFrame])) return nil;
    for (FyneA11yElement* c in [elementsFor(childIDs) reverseObjectEnumerator]) {
        id hit = [c accessibilityHitTest:point];
        if (hit) return hit;
    }
    return self;
}

- (BOOL)isAccessibilityElement { return YES; }
- (BOOL)isAccessibilityEnabled { return !(flags & MacA11yDisabled); }
- (BOOL)isAccessibilityFocused { return focusedID == eid; }
- (void)setAccessibilityFocused:(BOOL)focused {
    if (focused && (flags & MacA11yFocusable)) fyneA11yFocus(eid);
}
- (BOOL)isAccessibilitySelected { return (flags & MacA11ySelected) != 0; }
- (BOOL)isAccessibilityExpanded { return (flags & MacA11yExpanded) != 0; }

- (BOOL)accessibilityPerformPress {
    if (!(flags & MacA11yPress)) return NO;
    fyneA11yAction(eid, actionPress);
    return YES;
}
- (BOOL)accessibilityPerformIncrement {
    if (!(flags & MacA11yIncrement)) return NO;
    fyneA11yAction(eid, actionIncrement);
    return YES;
}
- (BOOL)accessibilityPerformDecrement {
    if (!(flags & MacA11yDecrement)) return NO;
    fyneA11yAction(eid, actionDecrement);
    return YES;
}
- (BOOL)accessibilityPerformShowMenu {
    if (!(flags & MacA11yShowMenu)) return NO;
    fyneA11yAction(eid, actionShowMenu);
    return YES;
}

// ---- text (NSAccessibility's parameterized attributes) ----

- (NSArray<NSNumber*>*)lineStarts {
    if (lines) return lines;
    NSMutableArray* starts = [NSMutableArray arrayWithObject:@0];
    NSUInteger n = [text length];
    for (NSUInteger i = 1; i < n; i++)
        if ([text characterAtIndex:i - 1] == '\n') [starts addObject:@(i)];
    return starts;
}

- (NSRange)clamp:(NSRange)r {
    NSUInteger n = [text length];
    NSUInteger loc = MIN(r.location, n);
    return NSMakeRange(loc, MIN(r.length, n - loc));
}

- (NSInteger)accessibilityNumberOfCharacters { return [text length]; }

- (NSRange)accessibilitySelectedTextRange {
    if (selStart != selEnd) return [self clamp:NSMakeRange(MIN(selStart, selEnd), labs(selEnd - selStart))];
    return [self clamp:NSMakeRange(caret >= 0 ? caret : 0, 0)];
}
- (void)setAccessibilitySelectedTextRange:(NSRange)range {
    fyneA11ySelect(eid, (int)range.location, (int)(range.location + range.length));
}
- (NSString*)accessibilitySelectedText {
    return text ? [text substringWithRange:[self accessibilitySelectedTextRange]] : nil;
}
- (NSRange)accessibilityVisibleCharacterRange { return NSMakeRange(0, [text length]); }

- (NSInteger)accessibilityLineForIndex:(NSInteger)index {
    NSArray<NSNumber*>* starts = [self lineStarts];
    NSInteger line = 0;
    for (NSUInteger i = 0; i < [starts count]; i++)
        if ([starts[i] integerValue] <= index) line = i;
    return line;
}
- (NSInteger)accessibilityInsertionPointLineNumber {
    return [self accessibilityLineForIndex:[self accessibilitySelectedTextRange].location];
}
- (NSRange)accessibilityRangeForLine:(NSInteger)line {
    NSArray<NSNumber*>* starts = [self lineStarts];
    if (line < 0 || line >= (NSInteger)[starts count]) return NSMakeRange(NSNotFound, 0);
    NSInteger start = [starts[line] integerValue];
    NSInteger end = line + 1 < (NSInteger)[starts count] ? [starts[line + 1] integerValue] : (NSInteger)[text length];
    return [self clamp:NSMakeRange(start, MAX(end - start, 0))];
}
- (NSString*)accessibilityStringForRange:(NSRange)range {
    return text ? [text substringWithRange:[self clamp:range]] : nil;
}
- (NSAttributedString*)accessibilityAttributedStringForRange:(NSRange)range {
    NSString* s = [self accessibilityStringForRange:range];
    return s ? [[[NSAttributedString alloc] initWithString:s] autorelease] : nil;
}
- (NSRange)accessibilityRangeForIndex:(NSInteger)index {
    if (!text || index < 0 || index >= (NSInteger)[text length]) return NSMakeRange(index, 0);
    return [text rangeOfComposedCharacterSequenceAtIndex:index];
}
- (NSRange)accessibilityStyleRangeForIndex:(NSInteger)index { return NSMakeRange(0, [text length]); }
- (NSRange)accessibilityRangeForPosition:(NSPoint)point { return NSMakeRange(0, 0); }
- (NSRect)accessibilityFrameForRange:(NSRange)range { return [self accessibilityFrame]; }

@end

// ---- GLFW's content view and window ----

static NSArray* viewChildren(id self, SEL _cmd) { return topElements(self); }
static NSAccessibilityRole viewRole(id self, SEL _cmd) { return NSAccessibilityGroupRole; }
static BOOL viewIsElement(id self, SEL _cmd) { return YES; }
static id viewFocused(id self, SEL _cmd) {
    FyneA11yElement* f = focusedID ? elementFor(@(focusedID)) : nil;
    return (f && f->window == [self window]) ? (id)f : self;
}
static id viewHitTest(id self, SEL _cmd, NSPoint point) {
    for (FyneA11yElement* e in [topElements(self) reverseObjectEnumerator]) {
        id hit = [e accessibilityHitTest:point];
        if (hit) return hit;
    }
    return self;
}
static NSArray* windowAXChildren(id self, SEL _cmd) {
    NSView* cv = [(NSWindow*)self contentView];
    return cv ? @[cv] : @[];
}

// hookWindow gives the classes of window and of its content view the
// methods above, once per class. class_replaceMethod adds them to that
// class only (NSView's and NSWindow's own stay as they are).
static void hookWindow(NSWindow* window) {
    static NSMutableSet* hooked;
    if (!hooked) hooked = [[NSMutableSet alloc] init];
    NSView* view = [window contentView];
    if (view && ![hooked containsObject:[view class]]) {
        Class c = [view class];
        [hooked addObject:c];
        class_replaceMethod(c, @selector(accessibilityChildren), (IMP)viewChildren, "@@:");
        class_replaceMethod(c, @selector(accessibilityRole), (IMP)viewRole, "@@:");
        class_replaceMethod(c, @selector(isAccessibilityElement), (IMP)viewIsElement, "B@:");
        class_replaceMethod(c, @selector(accessibilityFocusedUIElement), (IMP)viewFocused, "@@:");
        class_replaceMethod(c, @selector(accessibilityHitTest:), (IMP)viewHitTest, "@@:{CGPoint=dd}");
    }
    if (![hooked containsObject:[window class]]) {
        Class c = [window class];
        [hooked addObject:c];
        class_replaceMethod(c, @selector(accessibilityChildren), (IMP)windowAXChildren, "@@:");
    }
}

// ---- the API for Go ----

void MacA11yBegin(void) {
    if (!elements) {
        elements = [[NSMutableDictionary alloc] init];
        windowIDs = [[NSMutableDictionary alloc] init];
        windowChildren = [[NSMutableDictionary alloc] init];
        seen = [[NSMutableSet alloc] init];
    }
    [seen removeAllObjects];
}

static NSArray<NSNumber*>* numbers(const unsigned long long* v, int n) {
    NSMutableArray* out = [NSMutableArray arrayWithCapacity:n];
    for (int i = 0; i < n; i++) [out addObject:@(v[i])];
    return out;
}

static void layoutChanged(id of) {
    if (of) NSAccessibilityPostNotification(of, NSAccessibilityLayoutChangedNotification);
}

void MacA11yNode(unsigned long long id, unsigned long long parent, unsigned long long nsWindow,
    const char* role, const char* name, double x, double y, double width, double height, int flags,
    const unsigned long long* children, int childCount,
    const char* text, int caret, int selStart, int selEnd, const int* lines, int lineCount) {
    @autoreleasepool {
        NSWindow* window = (NSWindow*)(uintptr_t)nsWindow;
        NSNumber* key = @(id);
        NSArray<NSNumber*>* kids = numbers(children, childCount);
        [seen addObject:key];

        if (strcmp(role, "window") == 0) {
            if (window) hookWindow(window);
            windowIDs[[NSValue valueWithPointer:window]] = key;
            if (![windowChildren[key] isEqualToArray:kids]) {
                windowChildren[key] = kids;
                layoutChanged([window contentView]);
            }
            return;
        }

        FyneA11yElement* e = elements[key];
        BOOL isNew = e == nil;
        if (isNew) {
            e = [[[FyneA11yElement alloc] init] autorelease];
            e->eid = id;
            elements[key] = e;
        }
        e->parentID = parent;
        e->window = window;
        [e->role release];
        e->role = [[NSString alloc] initWithUTF8String:role];
        [e->label release];
        e->label = [[NSString alloc] initWithUTF8String:name ? name : ""];
        e->local = NSMakeRect(x, y, width, height);
        e->flags = flags;
        BOOL kidsChanged = !isNew && ![e->childIDs isEqualToArray:kids];
        [e->childIDs release];
        e->childIDs = [kids retain];
        [e->text release];
        e->text = text ? [[NSString alloc] initWithUTF8String:text] : nil;
        e->caret = caret;
        e->selStart = selStart;
        e->selEnd = selEnd;
        [e->lines release];
        e->lines = nil;
        if (lines && lineCount > 0) {
            NSMutableArray* l = [NSMutableArray arrayWithCapacity:lineCount];
            for (int i = 0; i < lineCount; i++) [l addObject:@(lines[i])];
            e->lines = [l retain];
        }
        if (isNew) NSAccessibilityPostNotification(e, NSAccessibilityCreatedNotification);
        if (kidsChanged) layoutChanged(e);
    }
}

void MacA11yEnd(void) {
    @autoreleasepool {
        for (NSNumber* key in [elements allKeys]) {
            if ([seen containsObject:key]) continue;
            FyneA11yElement* e = [[elements[key] retain] autorelease];
            [elements removeObjectForKey:key];
            NSAccessibilityPostNotification(e, NSAccessibilityUIElementDestroyedNotification);
            if (e->eid == focusedID) focusedID = 0;
        }
        for (NSNumber* key in [windowChildren allKeys]) {
            if (![seen containsObject:key]) [windowChildren removeObjectForKey:key];
        }
    }
}

void MacA11yFocus(unsigned long long id) {
    @autoreleasepool {
        focusedID = id;
        FyneA11yElement* e = elementFor(@(id));
        if (e) NSAccessibilityPostNotification(e, NSAccessibilityFocusedUIElementChangedNotification);
    }
}

void MacA11yTextChanged(unsigned long long id) {
    @autoreleasepool {
        FyneA11yElement* e = elementFor(@(id));
        if (e) NSAccessibilityPostNotification(e, NSAccessibilityValueChangedNotification);
    }
}

// (the caret moving too: VoiceOver reads and brailles from it)
void MacA11ySelectionChanged(unsigned long long id) {
    @autoreleasepool {
        FyneA11yElement* e = elementFor(@(id));
        if (e) NSAccessibilityPostNotification(e, NSAccessibilitySelectedTextChangedNotification);
    }
}

void MacA11yForgetWindow(unsigned long long nsWindow) {
    @autoreleasepool {
        [windowIDs removeObjectForKey:[NSValue valueWithPointer:(void*)(uintptr_t)nsWindow]];
    }
}

void* MacA11yElement(unsigned long long id) { return (void*)elementFor(@(id)); }
