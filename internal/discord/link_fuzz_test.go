package discord

import (
	"regexp"
	"testing"
)

// FuzzParseLink: a pasted webhook link. An accepted one gives a numeric id
// and a token of the characters Discord uses, nothing more.
func FuzzParseLink(f *testing.F) {
	for _, s := range []string{
		"https://discord.com/api/webhooks/123456789012345678/abcdefghijklmnopqrstuvwxyz_-0123",
		"https://discord.com/api/v10/webhooks/12345/abcdefghijklmnopqrstuvwx/",
		"http://discord.com/api/webhooks/1/2", "https://evil.example/api/webhooks/12345/abcdefghijklmnopqrstuvwx",
		"https://user@discord.com/api/webhooks/12345/abcdefghijklmnopqrstuvwx",
	} {
		f.Add(s)
	}
	id, token := regexp.MustCompile(`^\d{5,25}$`), regexp.MustCompile(`^[A-Za-z0-9_-]{20,100}$`)
	f.Fuzz(func(t *testing.T, link string) {
		i, tok, err := ParseLink(link)
		if err != nil {
			return
		}
		if !id.MatchString(i) || !token.MatchString(tok) {
			t.Fatalf("%q gave %q %q", link, i, tok)
		}
	})
}
