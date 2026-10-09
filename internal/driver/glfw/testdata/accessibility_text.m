// A probe of the macOS bridge, compiled on its own (no window, no Go): it
// gives the bridge snapshots and reads its elements as VoiceOver does.
#import <Cocoa/Cocoa.h>
#include <stdio.h>
#include <string.h>
#import "accessibility_darwin.h"

// Go's side, recorded
static char said[256];
void fyneA11yAction(unsigned long long id, int action) { snprintf(said, sizeof said, "action %llu %d", id, action); }
void fyneA11yFocus(unsigned long long id) { snprintf(said, sizeof said, "focus %llu", id); }
void fyneA11ySelect(unsigned long long id, int start, int end) { snprintf(said, sizeof said, "select %llu %d %d", id, start, end); }
void fyneA11ySetValue(unsigned long long id, char* value) { snprintf(said, sizeof said, "value %llu %s", id, value); }

static int failed;
#define CHECK(cond, ...) do { if (!(cond)) { fprintf(stderr, __VA_ARGS__); fprintf(stderr, "\n"); failed = 1; } } while (0)

// the script: "INT. BARN - DAY\n\nRain falls on the roof." shown wrapped
// after "Rain falls " (lines start at 0, 16, 17, 28)
static const char* script = "INT. BARN - DAY\n\nRain falls on the roof.";

static void snapshot(int withEditor, int caret) {
    unsigned long long winKids[] = {2, 3};
    unsigned long long winKidsWithout[] = {2};
    int lines[] = {0, 16, 17, 28};
    MacA11yBegin();
    MacA11yNode(1, 0, 0, "window", "Accolade", 0, 0, 800, 600, 0,
        withEditor ? winKids : winKidsWithout, withEditor ? 2 : 1, NULL, -1, 0, 0, NULL, 0);
    MacA11yNode(2, 1, 0, "button", "Save (Ctrl+S)", 0, 0, 40, 40, 1 | (1 << 4), NULL, 0, NULL, -1, 0, 0, NULL, 0);
    if (withEditor)
        MacA11yNode(3, 1, 0, "textArea", "Script", 0, 50, 700, 500, 1 | 4 | 8 | (1 << 11), NULL, 0,
            script, caret, caret, caret, lines, 4);
    MacA11yEnd();
}

int main(void) {
    @autoreleasepool {
        snapshot(1, 22);
        id button = (id)MacA11yElement(2);
        id editor = (id)MacA11yElement(3);
        CHECK(button && editor, "no elements");

        CHECK([[button accessibilityRole] isEqualToString:NSAccessibilityButtonRole], "button role %s", [[button accessibilityRole] UTF8String]);
        CHECK([[button accessibilityLabel] isEqualToString:@"Save (Ctrl+S)"], "button label");
        CHECK([[editor accessibilityRole] isEqualToString:NSAccessibilityTextAreaRole], "editor role %s", [[editor accessibilityRole] UTF8String]);
        CHECK([[editor accessibilityLabel] isEqualToString:@"Script"], "editor label");

        // the text, read by line as VoiceOver reads it
        CHECK([editor accessibilityNumberOfCharacters] == (NSInteger)strlen(script), "characters %ld", (long)[editor accessibilityNumberOfCharacters]);
        NSRange caret = [editor accessibilitySelectedTextRange];
        CHECK(caret.location == 22 && caret.length == 0, "caret %lu+%lu", (unsigned long)caret.location, (unsigned long)caret.length);
        CHECK([editor accessibilityInsertionPointLineNumber] == 2, "caret line %ld", (long)[editor accessibilityInsertionPointLineNumber]);
        CHECK([editor accessibilityLineForIndex:30] == 3, "line for 30: %ld", (long)[editor accessibilityLineForIndex:30]);
        NSRange line = [editor accessibilityRangeForLine:2];
        NSString* lineText = [editor accessibilityStringForRange:line];
        CHECK([lineText isEqualToString:@"Rain falls "], "line 2: '%s'", [lineText UTF8String]);
        CHECK([[editor accessibilityStringForRange:[editor accessibilityRangeForLine:0]] isEqualToString:@"INT. BARN - DAY\n"], "line 0");
        CHECK([editor accessibilityRangeForLine:9].location == NSNotFound, "a line past the end");
        CHECK([[[editor accessibilityAttributedStringForRange:NSMakeRange(0, 3)] string] isEqualToString:@"INT"], "attributed string");
        CHECK([[editor accessibilityValue] hasPrefix:@"INT. BARN"], "value");

        // the same element in the next snapshot: VoiceOver keeps following it
        snapshot(1, 5);
        CHECK((id)MacA11yElement(3) == editor, "the editor's element was replaced");
        CHECK([editor accessibilitySelectedTextRange].location == 5, "caret after the snapshot");

        // requests go to Go
        [button accessibilityPerformPress];
        CHECK(strcmp(said, "action 2 0") == 0, "press: %s", said);
        [editor setAccessibilityFocused:YES];
        CHECK(strcmp(said, "focus 3") == 0, "focus: %s", said);
        [editor setAccessibilitySelectedTextRange:NSMakeRange(17, 4)];
        CHECK(strcmp(said, "select 3 17 21") == 0, "select: %s", said);
        MacA11yFocus(3);
        CHECK([editor isAccessibilityFocused] && ![button isAccessibilityFocused], "focus");

        // gone from the snapshot: gone
        [editor retain];
        snapshot(0, 0);
        CHECK(MacA11yElement(3) == NULL, "the editor stayed");
        CHECK(![editor isAccessibilityFocused], "a gone element kept the focus");
        [editor release];
    }
    if (!failed) printf("ok\n");
    return failed;
}
