// Package filters catches spam in chat and acts on it as the streamer
// (D-085): links, capitals, symbols, emotes, repeats and the streamer's
// own words. Each catch is sent through the moderation record
// (docs/adr/0041-moderation-record.md), so it's sent once and kept with
// Twitch's answer, and adds a chat.filter.caught event for workflows.
package filters

import (
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode"
)

// Rules, in the order they're tried: the first that matches acts.
const (
	RuleWords   = "words"
	RuleLinks   = "links"
	RuleEmotes  = "emotes"
	RuleCaps    = "caps"
	RuleSymbols = "symbols"
	RuleRepeats = "repeats"
	// M8 slice 4 (D-108).
	RuleNewAccounts = "new_accounts"
	RuleArt         = "art"
)

// Order is the order rules are tried in.
var Order = []string{RuleWords, RuleNewAccounts, RuleLinks, RuleEmotes, RuleCaps, RuleSymbols, RuleArt, RuleRepeats}

// What a rule does. A filter never bans: a mistake would cost a viewer
// too much.
const (
	ActionDelete  = "delete"
	ActionTimeout = "timeout"
	ActionWarn    = "warn"
)

// Limits.
const (
	MaxTimeout   = 1209600 // two weeks, Twitch's longest timeout
	MaxWords     = 500
	MaxWordLen   = 100
	MaxAllowed   = 100
	RepeatWindow = 60 * time.Second
)

// ErrInvalid wraps settings that can't be saved.
var ErrInvalid = errors.New("filters: invalid")

func invalid(f string, a ...any) error { return fmt.Errorf("%w: %s", ErrInvalid, fmt.Sprintf(f, a...)) }

// Rule is what every rule has.
type Rule struct {
	On     bool   `json:"on"`
	Action string `json:"action"`
	// Seconds is a timeout's length; 0 for the other actions.
	Seconds           int  `json:"seconds"`
	ExemptVIPs        bool `json:"exempt_vips"`
	ExemptSubscribers bool `json:"exempt_subscribers"`
	// YouTubeSame makes a timeout a timeout on YouTube too (200 quota
	// units each); otherwise the rule only deletes there (D-100).
	YouTubeSame bool `json:"youtube_same"`
}

// YouTubeQuotaFloor: with fewer of today's YouTube quota units left, the
// filters only delete on YouTube, so a spam wave can't spend the rest.
const YouTubeQuotaFloor = 1000

// OnYouTube is what the rule does to a YouTube message (D-100): YouTube has
// no warnings, and a timeout costs ten deletes, so a rule deletes unless
// YouTubeSame is on and the quota allows. why says when it was changed.
func (r Rule) OnYouTube(quotaLeft int) (action string, seconds int, why string) {
	switch {
	case r.Action == ActionWarn:
		return ActionDelete, 0, "YouTube has no warnings, so it was deleted"
	case r.Action != ActionTimeout:
		return r.Action, r.Seconds, ""
	case !r.YouTubeSame:
		return ActionDelete, 0, ""
	case quotaLeft < YouTubeQuotaFloor:
		return ActionDelete, 0, fmt.Sprintf("fewer than %d YouTube quota units are left today, so it was deleted instead", YouTubeQuotaFloor)
	}
	return r.Action, r.Seconds, ""
}

// LinkRule catches web addresses, except to allowed sites.
type LinkRule struct {
	Rule
	// Allowed sites, like twitch.tv; their subdomains are allowed too.
	Allowed []string `json:"allowed"`
}

// CapsRule catches messages mostly in capitals.
type CapsRule struct {
	Rule
	MinLetters int `json:"min_letters"`
	Percent    int `json:"percent"`
}

// SymbolRule catches messages mostly of symbols.
type SymbolRule struct {
	Rule
	MinLength int `json:"min_length"`
	Percent   int `json:"percent"`
}

// EmoteRule catches messages with more than Max emotes.
type EmoteRule struct {
	Rule
	Max int `json:"max"`
}

// RepeatRule catches the same message from the same viewer Times times
// within a minute.
type RepeatRule struct {
	Rule
	Times int `json:"times"`
}

// NewAccountRule catches messages from Twitch accounts younger than Days
// days (D-108). YouTube gives no account age, so it never applies there.
type NewAccountRule struct {
	Rule
	Days int `json:"days"`
}

// ArtRule catches text art: at least MinChars of the characters such
// pictures are drawn with (Braille patterns, blocks, box drawing).
type ArtRule struct {
	Rule
	MinChars int `json:"min_chars"`
}

// WordRule catches the streamer's words and phrases.
type WordRule struct {
	Rule
	List []string `json:"list"`
}

// Settings are every filter's settings, saved as one.
type Settings struct {
	On      bool       `json:"on"`
	Words   WordRule   `json:"words"`
	Links   LinkRule   `json:"links"`
	Emotes  EmoteRule  `json:"emotes"`
	Caps    CapsRule   `json:"caps"`
	Symbols SymbolRule `json:"symbols"`
	Repeats RepeatRule `json:"repeats"`
	// M8 slice 4 (D-108): off until turned on.
	NewAccounts NewAccountRule `json:"new_accounts"`
	Art         ArtRule        `json:"art"`
}

// Defaults are the settings before any are saved: everything off.
func Defaults() Settings {
	r := Rule{Action: ActionDelete, ExemptVIPs: true}
	return Settings{
		Words:   WordRule{Rule: r, List: []string{}},
		Links:   LinkRule{Rule: r, Allowed: []string{"twitch.tv", "clips.twitch.tv"}},
		Emotes:  EmoteRule{Rule: r, Max: 10},
		Caps:    CapsRule{Rule: r, MinLetters: 10, Percent: 70},
		Symbols: SymbolRule{Rule: r, MinLength: 15, Percent: 50},
		Repeats: RepeatRule{Rule: r, Times: 3},
		// New accounts: subscribers are left alone by default too.
		NewAccounts: NewAccountRule{Rule: Rule{Action: ActionDelete, ExemptVIPs: true, ExemptSubscribers: true}, Days: 7},
		Art:         ArtRule{Rule: r, MinChars: 20},
	}
}

// rules are the rules in Order.
func (s *Settings) rules() []*Rule {
	return []*Rule{&s.Words.Rule, &s.NewAccounts.Rule, &s.Links.Rule, &s.Emotes.Rule, &s.Caps.Rule, &s.Symbols.Rule, &s.Art.Rule, &s.Repeats.Rule}
}

// RuleFor returns the named rule's common settings.
func (s Settings) RuleFor(name string) (Rule, bool) {
	i := slices.Index(Order, name)
	if i < 0 {
		return Rule{}, false
	}
	return *s.rules()[i], true
}

var (
	sitePattern  = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?(\.[a-z0-9]([a-z0-9-]*[a-z0-9])?)+$`)
	spaces       = regexp.MustCompile(`\s+`)
	inRange      = func(n, lo, hi int) bool { return n >= lo && n <= hi }
	friendlyName = map[string]string{RuleWords: "Words", RuleLinks: "Links", RuleEmotes: "Emotes", RuleCaps: "Capitals",
		RuleSymbols: "Symbols", RuleRepeats: "Repeats", RuleNewAccounts: "New accounts", RuleArt: "Text art"}
)

// Normalized checks the settings and returns them tidied: words trimmed,
// in lower case with single spaces, and sites in lower case, without
// duplicates.
func (s Settings) Normalized() (Settings, error) {
	for i, r := range s.rules() {
		name := friendlyName[Order[i]]
		switch r.Action {
		case ActionDelete, ActionWarn:
			if r.Seconds != 0 {
				return s, invalid("%s: only a timeout has a length", name)
			}
		case ActionTimeout:
			if !inRange(r.Seconds, 1, MaxTimeout) {
				return s, invalid("%s: a timeout is 1 second to 2 weeks", name)
			}
		default:
			return s, invalid("%s: a filter deletes, times out or warns", name)
		}
	}
	switch {
	case !inRange(s.Caps.MinLetters, 1, 500) || !inRange(s.Symbols.MinLength, 1, 500):
		return s, invalid("a message's length to judge is 1 to 500")
	case !inRange(s.Caps.Percent, 1, 100) || !inRange(s.Symbols.Percent, 1, 100):
		return s, invalid("a share is 1 to 100 percent")
	case !inRange(s.Emotes.Max, 1, 100):
		return s, invalid("Emotes: the most allowed is 1 to 100")
	case !inRange(s.Repeats.Times, 2, 10):
		return s, invalid("Repeats: the same message 2 to 10 times")
	case !inRange(s.NewAccounts.Days, 1, 365):
		return s, invalid("New accounts: younger than 1 to 365 days")
	case !inRange(s.Art.MinChars, 5, 500):
		return s, invalid("Text art: 5 to 500 drawing characters")
	case len(s.Words.List) > MaxWords:
		return s, invalid("up to %d words and phrases", MaxWords)
	case len(s.Links.Allowed) > MaxAllowed:
		return s, invalid("up to %d allowed sites", MaxAllowed)
	}
	words := make([]string, 0, len(s.Words.List))
	for _, w := range s.Words.List {
		w = strings.ToLower(spaces.ReplaceAllString(strings.TrimSpace(w), " "))
		core := strings.TrimSuffix(strings.TrimPrefix(w, "*"), "*")
		switch {
		case w == "" || core == "":
			return s, invalid("a word or phrase can't be empty")
		case len([]rune(w)) > MaxWordLen:
			return s, invalid("a word or phrase is up to %d characters", MaxWordLen)
		case strings.Contains(core, "*"):
			return s, invalid("%q: a * goes only at the start or end", w)
		}
		if !slices.Contains(words, w) {
			words = append(words, w)
		}
	}
	sites := make([]string, 0, len(s.Links.Allowed))
	for _, d := range s.Links.Allowed {
		d = strings.ToLower(strings.TrimSpace(d))
		if !sitePattern.MatchString(d) {
			return s, invalid("%q isn't a site: write it like twitch.tv, without https://", d)
		}
		if !slices.Contains(sites, d) {
			sites = append(sites, d)
		}
	}
	s.Words.List, s.Links.Allowed = words, sites
	return s, nil
}

// Check reports whether the settings can be saved.
func (s Settings) Check() error {
	_, err := s.Normalized()
	return err
}

// Reason is what Twitch is told, and so what a warned viewer and the
// moderators see.
func Reason(rule string) string {
	return map[string]string{
		RuleWords:       "That word isn't allowed here.",
		RuleLinks:       "Please don't post links here.",
		RuleEmotes:      "Please don't spam emotes.",
		RuleCaps:        "Please don't type in all capitals.",
		RuleSymbols:     "Please don't spam symbols.",
		RuleRepeats:     "Please don't repeat the same message.",
		RuleNewAccounts: "Please wait a few days: new accounts can't chat here yet.",
		RuleArt:         "Please don't post text art.",
	}[rule]
}

// Message is one chat message, as the filters judge it.
type Message struct {
	Platform      string
	UserID        string
	Login         string
	Role          string
	IsBroadcaster bool
	IsModerator   bool
	IsVIP         bool
	IsSubscriber  bool
	Text          string
	// Emotes are the emotes' names in the message, once per use.
	Emotes []string
	// AccountAgeDays is how old the Twitch account is, when known.
	AccountAgeDays *int
	At             time.Time
	// Permits, when set, are the viewers allowed to post links now.
	Permits *Permits
}

// Compiled are settings ready to judge with.
type Compiled struct {
	s     Settings
	words *regexp.Regexp // nil when there are none
}

// Settings returns the settings compiled.
func (c *Compiled) Settings() Settings { return c.s }

const wordChar = `[\p{L}\p{N}]`

// Compile checks the settings and readies them.
func Compile(s Settings) (*Compiled, error) {
	s, err := s.Normalized()
	if err != nil {
		return nil, err
	}
	c := &Compiled{s: s}
	if len(s.Words.List) > 0 {
		alts := make([]string, 0, len(s.Words.List))
		for _, w := range s.Words.List {
			core := strings.TrimSuffix(strings.TrimPrefix(w, "*"), "*")
			p := strings.ReplaceAll(regexp.QuoteMeta(core), " ", `\s+`)
			if strings.HasPrefix(w, "*") {
				p = wordChar + "*" + p
			}
			if strings.HasSuffix(w, "*") {
				p += wordChar + "*"
			}
			alts = append(alts, p)
		}
		c.words, err = regexp.Compile(`(?i)(?:^|[^\p{L}\p{N}])(?:` + strings.Join(alts, "|") + `)(?:$|[^\p{L}\p{N}])`)
		if err != nil {
			return nil, invalid("the words couldn't be read: %v", err)
		}
	}
	return c, nil
}

// exempt reports whether a rule leaves the message's sender alone. You
// and your moderators are never filtered.
func exempt(r Rule, m Message) bool {
	switch {
	case m.IsBroadcaster || m.Role == "broadcaster" || m.IsModerator || m.Role == "moderator":
		return true
	case r.ExemptVIPs && (m.IsVIP || m.Role == "vip"):
		return true
	case r.ExemptSubscribers && (m.IsSubscriber || m.Role == "subscriber"):
		return true
	}
	return false
}

// Judge returns the first rule that catches the message, or "". Repeats,
// when given, remembers the message for the Repeats rule.
func (c *Compiled) Judge(m Message, repeats *Repeats) string {
	s := c.s
	if !s.On {
		return ""
	}
	applies := func(r Rule) bool { return r.On && !exempt(r, m) }
	repeated := false
	if repeats != nil && applies(s.Repeats.Rule) {
		repeated = repeats.Add(m) >= s.Repeats.Times
	}
	plain := withoutEmotes(m.Text, m.Emotes)
	switch {
	case applies(s.Words.Rule) && c.words != nil && c.words.MatchString(m.Text):
		return RuleWords
	case applies(s.NewAccounts.Rule) && m.Platform != "youtube" && m.AccountAgeDays != nil && *m.AccountAgeDays < s.NewAccounts.Days:
		return RuleNewAccounts
	case applies(s.Links.Rule) && !m.Permits.Allowed(m.Platform, m.Login, m.At) && hasLink(m.Text, s.Links.Allowed):
		return RuleLinks
	case applies(s.Emotes.Rule) && len(m.Emotes) > s.Emotes.Max:
		return RuleEmotes
	case applies(s.Caps.Rule) && shouting(plain, s.Caps.MinLetters, s.Caps.Percent):
		return RuleCaps
	case applies(s.Symbols.Rule) && symbols(plain, s.Symbols.MinLength, s.Symbols.Percent):
		return RuleSymbols
	case applies(s.Art.Rule) && artChars(m.Text) >= s.Art.MinChars:
		return RuleArt
	case repeated:
		return RuleRepeats
	}
	return ""
}

// artChars counts the characters text art is drawn with: Braille
// patterns, block elements, box drawing, and the shade and geometric
// shapes often mixed in.
func artChars(text string) int {
	n := 0
	for _, r := range text {
		switch {
		case r >= 0x2800 && r <= 0x28FF, // Braille patterns
			r >= 0x2580 && r <= 0x259F, // block elements: ▀ ▄ █ ░ ▒ ▓
			r >= 0x2500 && r <= 0x257F, // box drawing
			r >= 0x25A0 && r <= 0x25FF: // geometric shapes
			n++
		}
	}
	return n
}

// withoutEmotes drops the emotes' names, so "KEKW" isn't shouting.
func withoutEmotes(text string, emotes []string) string {
	if len(emotes) == 0 {
		return text
	}
	left := map[string]int{}
	for _, e := range emotes {
		left[e]++
	}
	var keep []string
	for _, w := range strings.Fields(text) {
		if left[w] > 0 {
			left[w]--
			continue
		}
		keep = append(keep, w)
	}
	return strings.Join(keep, " ")
}

func shouting(text string, minLetters, percent int) bool {
	letters, upper := 0, 0
	for _, r := range text {
		if unicode.IsLetter(r) {
			letters++
			if unicode.IsUpper(r) {
				upper++
			}
		}
	}
	return letters >= minLetters && upper*100 >= percent*letters
}

func symbols(text string, minLength, percent int) bool {
	n, sym := 0, 0
	for _, r := range text {
		if unicode.IsSpace(r) {
			continue
		}
		n++
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) {
			sym++
		}
	}
	return n >= minLength && sym*100 >= percent*n
}

// linkTLDs are the endings a bare name must have to count as a link, so
// "ok.so" and "e.g" don't. Any name after http:// or www. counts.
var linkTLDs = map[string]bool{}

func init() {
	for _, t := range strings.Fields(`com net org info biz io gg tv ly me co app dev xyz live site online store shop link click
		top club fun ru cn uk de fr nl eu ca au jp br pl ch se tk ml ga cf gq pw cc ws sh ai gl gd vip win bid loan
		stream fans social pro page`) {
		linkTLDs[t] = true
	}
}

var hostPattern = regexp.MustCompile(`(?i)(https?://)?((?:[a-z0-9-]+\.)+[a-z0-9-]+)`)

// hasLink reports whether text has a web address to a site not allowed.
func hasLink(text string, allowed []string) bool {
	for _, m := range hostPattern.FindAllStringSubmatch(text, -1) {
		host := strings.ToLower(m[2])
		if m[1] != "" {
			if u, err := url.Parse(strings.ToLower(m[0])); err == nil && u.Hostname() != "" {
				host = u.Hostname()
			}
		} else if !strings.HasPrefix(host, "www.") && !linkTLDs[host[strings.LastIndexByte(host, '.')+1:]] {
			continue
		}
		ok := false
		for _, d := range allowed {
			if host == d || strings.HasSuffix(host, "."+d) {
				ok = true
				break
			}
		}
		if !ok {
			return true
		}
	}
	return false
}

// Repeats remembers each viewer's messages for a minute.
type Repeats struct {
	mu   sync.Mutex
	seen map[string][]seenMsg
}

type seenMsg struct {
	text string
	at   time.Time
}

// NewRepeats returns an empty memory.
func NewRepeats() *Repeats { return &Repeats{seen: map[string][]seenMsg{}} }

// Add remembers the message and returns how many times the viewer sent
// it within the last minute, this one included.
func (r *Repeats) Add(m Message) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	key := m.Platform + ":" + m.UserID
	text := strings.ToLower(spaces.ReplaceAllString(strings.TrimSpace(m.Text), " "))
	keep := r.seen[key][:0]
	n := 1
	for _, s := range r.seen[key] {
		if m.At.Sub(s.at) < RepeatWindow {
			keep = append(keep, s)
			if s.text == text {
				n++
			}
		}
	}
	r.seen[key] = append(keep, seenMsg{text: text, at: m.At})
	if len(r.seen) > 10000 { // forget viewers who've gone quiet
		for k, v := range r.seen {
			if len(v) == 0 || m.At.Sub(v[len(v)-1].at) >= RepeatWindow {
				delete(r.seen, k)
			}
		}
	}
	return n
}

// Permits are viewers allowed to post links for a while (!permit).
type Permits struct {
	mu    sync.Mutex
	until map[string]time.Time
}

// NewPermits returns no permits.
func NewPermits() *Permits { return &Permits{until: map[string]time.Time{}} }

func permitKey(platform, login string) string {
	return platform + ":" + strings.ToLower(strings.TrimPrefix(strings.TrimSpace(login), "@"))
}

// Allow lets login post links for d from now.
func (p *Permits) Allow(platform, login string, d time.Duration, now time.Time) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for k, t := range p.until {
		if !now.Before(t) {
			delete(p.until, k)
		}
	}
	p.until[permitKey(platform, login)] = now.Add(d)
}

// Allowed reports whether login may post links at t. A nil Permits
// allows nobody.
func (p *Permits) Allowed(platform, login string, t time.Time) bool {
	if p == nil {
		return false
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return t.Before(p.until[permitKey(platform, login)])
}
