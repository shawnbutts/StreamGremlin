package chat

import (
	"strings"
	"testing"
)

// FuzzParseCommand: chat is anyone's input. An accepted command has a
// valid lowercase name and trimmed arguments, and its words are the
// arguments' fields (M9 slice 1).
func FuzzParseCommand(f *testing.F) {
	for _, s := range []string{"!hype", "!so @friend", "  !Points  give  10 ", "!", "!!", "hello", "!a\tb", "!é", "!x y"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, text string) {
		c, ok := ParseCommand(text)
		if !ok {
			return
		}
		if !commandName.MatchString(c.Name) {
			t.Fatalf("name %q", c.Name)
		}
		if c.Args != strings.TrimSpace(c.Args) {
			t.Fatalf("args %q aren't trimmed", c.Args)
		}
		if strings.Join(c.ArgList, " ") != strings.Join(strings.Fields(c.Args), " ") {
			t.Fatalf("arg list %q from %q", c.ArgList, c.Args)
		}
		again, ok := ParseCommand("!" + c.Name + " " + c.Args)
		if !ok || again.Name != c.Name || again.Args != c.Args {
			t.Fatalf("%q read again as %+v", text, again)
		}
	})
}
