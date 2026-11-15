package banlists

import (
	"strings"
	"testing"
)

// FuzzParseName: a ban list's names come from any file. An accepted name
// is a valid login or user id, and reads back as itself.
func FuzzParseName(f *testing.F) {
	for _, s := range []string{"someone", "@Someone", `"12345"`, " id ", "a b", "ÿ", "id:12", strings.Repeat("a", 26)} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, v string) {
		n, ok := ParseName(v)
		if !ok {
			return
		}
		if !loginPattern.MatchString(n.Value()) && !idPattern.MatchString(n.Value()) {
			t.Fatalf("%q gave %q", v, n)
		}
		if again, ok := ParseName(n.Value()); !ok || again != n {
			t.Fatalf("%q read back as %q", n.Value(), again)
		}
	})
}

// FuzzRead: a whole file. Never more names than lines, and every name is
// valid.
func FuzzRead(f *testing.F) {
	for _, s := range []string{"a\nb\n", "login,reason\nfoo,spam\n", "\ufeffuser_id\tname\n1\tx\n", "# note\n\n@x;y", "\r\n\r\n"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		names := 0
		_, err := Read(strings.NewReader(s), func(n Name) error {
			names++
			if !loginPattern.MatchString(n.Value()) && !idPattern.MatchString(n.Value()) {
				t.Fatalf("name %q", n)
			}
			return nil
		})
		if err != nil {
			return
		}
		if lines := strings.Count(s, "\n") + 1; names > lines {
			t.Fatalf("%d names from %d lines", names, lines)
		}
	})
}
