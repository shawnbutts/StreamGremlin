package filters

import (
	"errors"
	"strings"
	"testing"
	"time"
)

// allOn is every rule on, deleting, with the default settings otherwise.
func allOn() Settings {
	s := Defaults()
	s.On = true
	for _, r := range s.rules() {
		r.On = true
	}
	s.Words.List = []string{"badword", "spoil*", "*phrase thing"}
	return s
}

func viewer(text string) Message {
	return Message{Platform: "twitch", UserID: "42", Login: "someone", Role: "viewer", Text: text}
}

func TestDefaultsAreOffAndValid(t *testing.T) {
	s := Defaults()
	if s.On {
		t.Error("filters start on")
	}
	for name, r := range map[string]*Rule{"links": &s.Links.Rule, "caps": &s.Caps.Rule, "symbols": &s.Symbols.Rule,
		"emotes": &s.Emotes.Rule, "repeats": &s.Repeats.Rule, "words": &s.Words.Rule} {
		if r.On || r.Action != ActionDelete || !r.ExemptVIPs || r.ExemptSubscribers {
			t.Errorf("%s: %+v", name, *r)
		}
	}
	if err := s.Check(); err != nil {
		t.Fatal(err)
	}
	if _, err := Compile(s); err != nil {
		t.Fatal(err)
	}
}

func judge(t *testing.T, s Settings, m Message) string {
	t.Helper()
	c, err := Compile(s)
	if err != nil {
		t.Fatal(err)
	}
	return c.Judge(m, nil)
}

func TestLinks(t *testing.T) {
	s := allOn()
	for _, text := range []string{
		"check https://example.org/x", "go to www.example.net now", "bit.ly/abc", "FREE FOLLOWERS at cheap-viewers.xyz",
		"t.me/scam", "HTTP://EXAMPLE.COM", "see sub.domain.co.uk/path?q=1",
	} {
		if got := judge(t, s, viewer(text)); got != RuleLinks {
			t.Errorf("%q: %q, want links", text, got)
		}
	}
	for _, text := range []string{
		"ok.so what now", "e.g. this", "I scored 3.5 today", "twitch.tv/friend is great", "https://clips.twitch.tv/SomeClip",
		"www.twitch.tv/videos/1", "hello...world", "file.txt", "",
	} {
		if got := judge(t, s, viewer(text)); got != "" {
			t.Errorf("%q: caught by %q", text, got)
		}
	}
	// A look-alike isn't an allowed site.
	if got := judge(t, s, viewer("eviltwitch.tv/x")); got != RuleLinks {
		t.Errorf("eviltwitch.tv: %q", got)
	}
	// An allowed site of the streamer's own.
	s.Links.Allowed = append(s.Links.Allowed, "youtube.com")
	if got := judge(t, s, viewer("https://www.youtube.com/watch?v=1")); got != "" {
		t.Errorf("allowed youtube.com: %q", got)
	}
}

func TestCapsAndSymbols(t *testing.T) {
	s := allOn()
	cases := map[string]string{
		"WHY IS THIS SO LOUD":       RuleCaps,
		"OK":                        "", // too short
		"Hello There Everyone":      "", // under 70%
		"!!!!!!!!!!!!!!!!!!!!":      RuleSymbols,
		"hi :) how are you?":        "",       // few symbols
		"ÉCOUTEZ TOUT LE MONDE ICI": RuleCaps, // capitals in any alphabet
	}
	for text, want := range cases {
		if got := judge(t, s, viewer(text)); got != want {
			t.Errorf("%q: %q, want %q", text, got, want)
		}
	}
	// Emotes' names aren't shouting: "KEKW KEKW KEKW KEKW" with 4 emotes.
	m := viewer("KEKW KEKW KEKW KEKW lol")
	m.Emotes = []string{"KEKW", "KEKW", "KEKW", "KEKW"}
	if got := judge(t, s, m); got != "" {
		t.Errorf("emote names counted as capitals: %q", got)
	}
}

func TestEmotes(t *testing.T) {
	s := allOn()
	m := viewer(strings.Repeat("Kappa ", 11))
	m.Emotes = strings.Fields(m.Text)
	if got := judge(t, s, m); got != RuleEmotes {
		t.Errorf("11 emotes: %q", got)
	}
	m.Emotes = m.Emotes[:10]
	m.Text = strings.Repeat("Kappa ", 10)
	if got := judge(t, s, m); got != "" {
		t.Errorf("10 emotes: %q", got)
	}
}

func TestWords(t *testing.T) {
	s := allOn()
	for text, want := range map[string]string{
		"that is a BADWORD ok":                 RuleWords,
		"badwords":                             "", // a whole word only
		"no spoilers please":                   RuleWords,
		"spoil":                                RuleWords,
		"unspoiled":                            "",
		"the big phrase   thing here":          RuleWords, // any spaces between
		"a bigphrase thing":                    RuleWords, // * at the start
		"phrase thingy":                        "",
		"badword.":                             RuleWords,
		"Words before links: badword bit.ly/x": RuleWords,
	} {
		if got := judge(t, s, viewer(text)); got != want {
			t.Errorf("%q: %q, want %q", text, got, want)
		}
	}
	// Regular-expression characters are only text.
	s.Words.List = []string{"a.b", "(x)"}
	if got := judge(t, s, viewer("aXb")); got != "" {
		t.Errorf("a.b matched aXb")
	}
	if got := judge(t, s, viewer("say (x) now")); got != RuleWords {
		t.Errorf("(x): %q", got)
	}
}

func TestNeverFiltered(t *testing.T) {
	s := allOn()
	s.Links.ExemptSubscribers = true
	for _, m := range []Message{
		{Role: "broadcaster", Text: "bit.ly/x"},
		{Role: "moderator", IsModerator: true, Text: "bit.ly/x"},
		{Role: "vip", IsVIP: true, Text: "bit.ly/x"},
		{Role: "subscriber", IsSubscriber: true, Text: "bit.ly/x"},
		// A moderator who's also a subscriber is shown as a moderator.
		{Role: "viewer", IsModerator: true, Text: "bit.ly/x"},
	} {
		if got := judge(t, s, m); got != "" {
			t.Errorf("%+v: caught by %q", m, got)
		}
	}
	// A subscriber is still caught by a rule that doesn't leave them alone.
	if got := judge(t, s, Message{Role: "subscriber", IsSubscriber: true, Text: "badword"}); got != RuleWords {
		t.Errorf("subscriber's word: %q", got)
	}
	// All off: nothing.
	s.On = false
	if got := judge(t, s, viewer("badword bit.ly/x")); got != "" {
		t.Errorf("off: %q", got)
	}
}

func TestRepeats(t *testing.T) {
	s := allOn()
	c, err := Compile(s)
	if err != nil {
		t.Fatal(err)
	}
	r := NewRepeats()
	at := time.Unix(1_800_000_000, 0)
	m := viewer("buy my stuff")
	for i, want := range []string{"", "", RuleRepeats, RuleRepeats} {
		m.At = at.Add(time.Duration(i) * 10 * time.Second)
		if got := c.Judge(m, r); got != want {
			t.Errorf("message %d: %q, want %q", i+1, got, want)
		}
	}
	// Case and spacing don't make it new; someone else's copy is theirs.
	other := viewer("buy my stuff")
	other.UserID, other.At = "43", at.Add(time.Second)
	if got := c.Judge(other, r); got != "" {
		t.Errorf("another viewer: %q", got)
	}
	// After 60 quiet seconds it starts over.
	m.At = at.Add(5 * time.Minute)
	m.Text = "BUY  my stuff"
	if got := c.Judge(m, r); got != "" {
		t.Errorf("after a minute: %q", got)
	}
	m.At = m.At.Add(time.Second)
	if got := c.Judge(m, r); got != "" {
		t.Errorf("second after a minute: %q", got)
	}
	m.At = m.At.Add(time.Second)
	if got := c.Judge(m, r); got != RuleRepeats {
		t.Errorf("third, different case and spaces: %q", got)
	}
}

func TestPermit(t *testing.T) {
	s := allOn()
	c, err := Compile(s)
	if err != nil {
		t.Fatal(err)
	}
	at := time.Unix(1_800_000_000, 0)
	p := NewPermits()
	p.Allow("twitch", "@SomeOne", time.Minute, at)
	m := viewer("bit.ly/x")
	m.At, m.Permits = at.Add(30*time.Second), p
	if got := c.Judge(m, nil); got != "" {
		t.Errorf("permitted: %q", got)
	}
	// A permit is for links only.
	m.Text = "bit.ly/x badword"
	if got := c.Judge(m, nil); got != RuleWords {
		t.Errorf("permit let a word through: %q", got)
	}
	m.Text, m.At = "bit.ly/x", at.Add(61*time.Second)
	if got := c.Judge(m, nil); got != RuleLinks {
		t.Errorf("expired permit: %q", got)
	}
}

func TestCheck(t *testing.T) {
	bad := []func(*Settings){
		func(s *Settings) { s.Links.Action = "ban" },
		func(s *Settings) { s.Caps.Action, s.Caps.Seconds = ActionTimeout, 0 },
		func(s *Settings) { s.Caps.Action, s.Caps.Seconds = ActionTimeout, 1209601 },
		func(s *Settings) { s.Caps.Seconds = 60 }, // only a timeout has a length
		func(s *Settings) { s.Caps.Percent = 0 },
		func(s *Settings) { s.Caps.Percent = 101 },
		func(s *Settings) { s.Caps.MinLetters = 0 },
		func(s *Settings) { s.Symbols.MinLength = 501 },
		func(s *Settings) { s.Emotes.Max = 0 },
		func(s *Settings) { s.Repeats.Times = 1 },
		func(s *Settings) { s.Repeats.Times = 11 },
		func(s *Settings) { s.Words.List = []string{""} },
		func(s *Settings) { s.Words.List = []string{"a*b"} },
		func(s *Settings) { s.Words.List = []string{"*"} },
		func(s *Settings) { s.Words.List = []string{strings.Repeat("a", 101)} },
		func(s *Settings) { s.Words.List = make([]string, 501) },
		func(s *Settings) { s.Links.Allowed = []string{"not a site"} },
		func(s *Settings) { s.Links.Allowed = []string{"https://twitch.tv"} },
		func(s *Settings) { s.Links.Allowed = make([]string, 101) },
	}
	for i, f := range bad {
		s := Defaults()
		f(&s)
		if err := s.Check(); !errors.Is(err, ErrInvalid) {
			t.Errorf("case %d accepted: %v", i, err)
		}
	}
	s := Defaults()
	s.Words.List = []string{" Spaced  Out ", "ok"}
	s.Links.Allowed = []string{"YouTube.com"}
	s.Caps.Action, s.Caps.Seconds = ActionTimeout, 600
	n, err := s.Normalized()
	if err != nil {
		t.Fatal(err)
	}
	if n.Words.List[0] != "spaced out" || n.Links.Allowed[0] != "youtube.com" {
		t.Errorf("normalized: %+v %+v", n.Words.List, n.Links.Allowed)
	}
}

func TestReasonsAndOrder(t *testing.T) {
	if len(Order) != 6 || Order[0] != RuleWords || Order[1] != RuleLinks {
		t.Fatalf("order %v", Order)
	}
	for _, r := range Order {
		if !strings.HasPrefix(Reason(r), "Please") && !strings.HasPrefix(Reason(r), "That") {
			t.Errorf("%s: %q", r, Reason(r))
		}
	}
}
