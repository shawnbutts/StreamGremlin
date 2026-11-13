// Package discord posts to Discord channels through webhooks (D-089): a
// link Discord makes for one channel, which needs no bot account. Each
// link is a secret: it's kept in the keyring, never in settings, logs or
// API answers.
package discord

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"code.streamgremlin.com/streamgremlin/internal/secrets"
	"code.streamgremlin.com/streamgremlin/internal/settings"
)

// Errors.
var (
	// ErrInvalid: the request can't be sent as it is.
	ErrInvalid = errors.New("discord: invalid")
	// ErrNoWebhook: no webhook by that name is saved.
	ErrNoWebhook = errors.New("discord: no webhook by that name: add it on Connections")
	// ErrWebhookGone: Discord doesn't know the webhook any more (deleted
	// in Discord, or a wrong link).
	ErrWebhookGone = errors.New("discord: Discord doesn't know this webhook: it was deleted or the link is wrong. Add it again on Connections")
	// ErrRefused: Discord refused the message, with its reason.
	ErrRefused = errors.New("discord: Discord refused it")
	// ErrOutcomeUnknown: Discord didn't answer, so whether the message was
	// posted is unknown. It's never sent again.
	ErrOutcomeUnknown = errors.New("discord: Discord didn't answer, so the message may or may not have been posted; it wasn't sent again")
)

// Limits from Discord's reference.
const (
	MaxContent     = 2000
	MaxTitle       = 256
	MaxDescription = 4096
	MaxFooter      = 2048
	MaxEmbedTotal  = 6000
	MaxWebhooks    = 20
)

const (
	settingsKey = "discord"
	// DefaultBase is Discord's address; tests use mockdiscord's.
	DefaultBase = "https://discord.com"
)

// Webhook is a saved webhook. Its link (with Discord's token) is a secret
// and isn't here.
type Webhook struct {
	// Name is the streamer's name for it, used by workflows: "announcements".
	Name string `json:"name"`
	// ID is Discord's id for the webhook; it isn't secret.
	ID string `json:"id"`
	// DiscordName is the webhook's own name in Discord, which posts show.
	DiscordName string    `json:"discord_name"`
	ChannelID   string    `json:"channel_id"`
	GuildID     string    `json:"guild_id,omitempty"`
	AddedAt     time.Time `json:"added_at"`
}

type saved struct {
	Webhooks []Webhook `json:"webhooks"`
}

// Options configure a Service.
type Options struct {
	Settings *settings.Store
	Secrets  *secrets.Store
	Logger   *slog.Logger
	// Base is Discord's address; empty means DefaultBase.
	Base string
	HTTP *http.Client
	// Now is the clock; nil means time.Now.
	Now func() time.Time
}

// Service holds the webhooks and sends to them.
type Service struct {
	settings *settings.Store
	secrets  *secrets.Store
	log      *slog.Logger
	base     string
	http     *http.Client
	now      func() time.Time
	mu       sync.Mutex // one change to the list at a time
	// waits caps how long a rate-limited send waits before trying again.
	maxWait time.Duration
}

// New makes a Service.
func New(o Options) *Service {
	s := &Service{settings: o.Settings, secrets: o.Secrets, log: o.Logger, base: strings.TrimRight(o.Base, "/"), http: o.HTTP, now: o.Now, maxWait: 10 * time.Second}
	if s.log == nil {
		s.log = slog.New(slog.DiscardHandler)
	}
	if s.base == "" {
		s.base = DefaultBase
	}
	if s.http == nil {
		s.http = &http.Client{Timeout: 15 * time.Second}
	}
	if s.now == nil {
		s.now = time.Now
	}
	return s
}

var (
	namePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,31}$`)
	// A webhook link as Discord copies it: https, a Discord host, an
	// optional API version, then the id and token.
	linkPattern  = regexp.MustCompile(`^/api(?:/v\d+)?/webhooks/(\d{5,25})/([A-Za-z0-9_-]{20,100})/?$`)
	discordHosts = []string{"discord.com", "discordapp.com", "canary.discord.com", "ptb.discord.com"}
)

func secretKey(name string) string { return "discord/webhook/" + name }

// ParseLink checks a webhook link and returns its id and token.
func ParseLink(link string) (id, token string, err error) {
	u, err := url.Parse(strings.TrimSpace(link))
	if err != nil || u.Scheme != "https" || !slices.Contains(discordHosts, strings.ToLower(u.Host)) || u.User != nil {
		return "", "", fmt.Errorf("%w: paste the webhook link Discord copies, starting https://discord.com/api/webhooks/", ErrInvalid)
	}
	m := linkPattern.FindStringSubmatch(u.Path)
	if m == nil {
		return "", "", fmt.Errorf("%w: that isn't a whole webhook link: it ends with /api/webhooks/ then two parts", ErrInvalid)
	}
	return m[1], m[2], nil
}

// Webhooks lists the saved webhooks, by name.
func (s *Service) Webhooks(ctx context.Context) ([]Webhook, error) {
	var sv saved
	if _, err := s.settings.Load(ctx, settingsKey, &sv); err != nil {
		return nil, err
	}
	if sv.Webhooks == nil {
		sv.Webhooks = []Webhook{}
	}
	slices.SortFunc(sv.Webhooks, func(a, b Webhook) int { return strings.Compare(a.Name, b.Name) })
	return sv.Webhooks, nil
}

// Add checks a link with Discord and saves it under name, replacing a
// webhook saved under that name before.
func (s *Service) Add(ctx context.Context, name, link string) (Webhook, error) {
	name = strings.TrimSpace(name)
	if !namePattern.MatchString(name) {
		return Webhook{}, fmt.Errorf("%w: a name is 1 to 32 lower-case letters, digits and dashes, like announcements", ErrInvalid)
	}
	id, token, err := ParseLink(link)
	if err != nil {
		return Webhook{}, err
	}
	var info struct {
		ID        string `json:"id"`
		Name      string `json:"name"`
		ChannelID string `json:"channel_id"`
		GuildID   string `json:"guild_id"`
	}
	if err := s.do(ctx, "GET", id, token, "", nil, &info); err != nil {
		return Webhook{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var sv saved
	if _, err := s.settings.Load(ctx, settingsKey, &sv); err != nil {
		return Webhook{}, err
	}
	sv.Webhooks = slices.DeleteFunc(sv.Webhooks, func(w Webhook) bool { return w.Name == name })
	if len(sv.Webhooks) >= MaxWebhooks {
		return Webhook{}, fmt.Errorf("%w: up to %d webhooks", ErrInvalid, MaxWebhooks)
	}
	if err := s.secrets.Set(ctx, secretKey(name), secrets.NewValue(id+"/"+token)); err != nil {
		return Webhook{}, err
	}
	w := Webhook{Name: name, ID: id, DiscordName: info.Name, ChannelID: info.ChannelID, GuildID: info.GuildID, AddedAt: s.now().UTC()}
	sv.Webhooks = append(sv.Webhooks, w)
	if err := s.settings.Save(ctx, settingsKey, sv); err != nil {
		return Webhook{}, err
	}
	s.log.Info("discord webhook saved", "name", name, "webhook_id", id)
	return w, nil
}

// Remove forgets a webhook here. It stays in Discord.
func (s *Service) Remove(ctx context.Context, name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	var sv saved
	if _, err := s.settings.Load(ctx, settingsKey, &sv); err != nil {
		return err
	}
	n := len(sv.Webhooks)
	sv.Webhooks = slices.DeleteFunc(sv.Webhooks, func(w Webhook) bool { return w.Name == name })
	if len(sv.Webhooks) == n {
		return ErrNoWebhook
	}
	if err := s.settings.Save(ctx, settingsKey, sv); err != nil {
		return err
	}
	if err := s.secrets.Delete(ctx, secretKey(name)); err != nil && !errors.Is(err, secrets.ErrNotFound) {
		return err
	}
	s.log.Info("discord webhook forgotten", "name", name)
	return nil
}

// Mention is who a message may notify.
type Mention string

// Mentions.
const (
	// MentionNone notifies nobody, whatever the text says: a viewer's
	// name or message can't ping anyone.
	MentionNone Mention = ""
	// MentionEveryone puts @everyone first and lets it notify.
	MentionEveryone Mention = "everyone"
	// MentionHere puts @here first and lets it notify.
	MentionHere Mention = "here"
)

// Embed is a message's card.
type Embed struct {
	Title       string `json:"title,omitempty"`
	Description string `json:"description,omitempty"`
	URL         string `json:"url,omitempty"`
	// Color is "#rrggbb", or empty.
	Color     string `json:"color,omitempty"`
	Image     string `json:"image,omitempty"`
	Thumbnail string `json:"thumbnail,omitempty"`
	Footer    string `json:"footer,omitempty"`
}

func (e Embed) empty() bool {
	return e.Title == "" && e.Description == "" && e.Image == "" && e.Thumbnail == "" && e.Footer == ""
}

// Message is what to post.
type Message struct {
	Content string
	Embed   Embed
	// Username, when set, shows instead of the webhook's name.
	Username string
	Mention  Mention
}

// Sent is Discord's answer to a post.
type Sent struct {
	MessageID string `json:"message_id"`
	ChannelID string `json:"channel_id"`
	// Truncated is set when text was cut to fit Discord's limits.
	Truncated bool `json:"truncated,omitempty"`
}

var colorPattern = regexp.MustCompile(`^#?[0-9a-fA-F]{6}$`)

// cut shortens s to max characters, ending with … when it cut.
func cut(s string, max int, cutAny *bool) string {
	if utf8.RuneCountInString(s) <= max {
		return s
	}
	*cutAny = true
	r := []rune(s)
	return string(r[:max-1]) + "…"
}

func link(raw, what string) (string, error) {
	if raw == "" {
		return "", nil
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
		return "", fmt.Errorf("%w: the %s is a web link starting https://", ErrInvalid, what)
	}
	return raw, nil
}

// body builds Execute Webhook's JSON. Text is cut to Discord's limits
// rather than refused, since a viewer's name or message can be long.
func (m Message) body() (map[string]any, bool, error) {
	truncated := false
	content := strings.TrimSpace(m.Content)
	parse := []string{}
	switch m.Mention {
	case MentionNone:
	case MentionEveryone, MentionHere:
		content = strings.TrimSpace("@" + string(m.Mention) + " " + content)
		parse = []string{"everyone"} // covers @here too
	default:
		return nil, false, fmt.Errorf("%w: mention is none, everyone or here", ErrInvalid)
	}
	content = cut(content, MaxContent, &truncated)
	out := map[string]any{"allowed_mentions": map[string]any{"parse": parse}}
	if content != "" {
		out["content"] = content
	}
	if u := strings.TrimSpace(m.Username); u != "" {
		out["username"] = cut(u, 80, &truncated)
	}
	e := m.Embed
	for _, p := range []*string{&e.Title, &e.Description, &e.URL, &e.Color, &e.Image, &e.Thumbnail, &e.Footer} {
		*p = strings.TrimSpace(*p)
	}
	if !e.empty() {
		em := map[string]any{}
		e.Title = cut(e.Title, MaxTitle, &truncated)
		e.Footer = cut(e.Footer, MaxFooter, &truncated)
		room := MaxEmbedTotal - utf8.RuneCountInString(e.Title) - utf8.RuneCountInString(e.Footer)
		e.Description = cut(e.Description, min(MaxDescription, room), &truncated)
		if e.Title != "" {
			em["title"] = e.Title
		}
		if e.Description != "" {
			em["description"] = e.Description
		}
		if e.Footer != "" {
			em["footer"] = map[string]string{"text": e.Footer}
		}
		var err error
		if e.URL, err = link(e.URL, "card's link"); err != nil {
			return nil, false, err
		}
		if e.URL != "" {
			em["url"] = e.URL
		}
		for _, img := range []struct{ field, v, what string }{{"image", e.Image, "picture"}, {"thumbnail", e.Thumbnail, "small picture"}} {
			v, err := link(img.v, img.what)
			if err != nil {
				return nil, false, err
			}
			if v != "" {
				em[img.field] = map[string]string{"url": v}
			}
		}
		if e.Color != "" {
			if !colorPattern.MatchString(e.Color) {
				return nil, false, fmt.Errorf("%w: a colour is like #9146ff", ErrInvalid)
			}
			n, _ := strconv.ParseInt(strings.TrimPrefix(e.Color, "#"), 16, 32)
			em["color"] = n
		}
		out["embeds"] = []any{em}
	}
	if _, ok := out["content"]; !ok && out["embeds"] == nil {
		return nil, false, fmt.Errorf("%w: say something: a message, or a card with a title or text", ErrInvalid)
	}
	return out, truncated, nil
}

// Send posts m through the webhook saved as name. A rate-limited post
// (429, so not posted) waits as Discord says and is tried again, twice at
// most; anything else is sent once.
func (s *Service) Send(ctx context.Context, name string, m Message) (Sent, error) {
	body, truncated, err := m.body()
	if err != nil {
		return Sent{}, err
	}
	id, token, err := s.credentials(ctx, name)
	if err != nil {
		return Sent{}, err
	}
	var out struct {
		ID        string `json:"id"`
		ChannelID string `json:"channel_id"`
	}
	if err := s.do(ctx, "POST", id, token, "wait=true", body, &out); err != nil {
		s.log.Warn("discord post failed", "webhook", name, "error", err)
		return Sent{}, err
	}
	s.log.Info("discord post sent", "webhook", name, "message_id", out.ID)
	return Sent{MessageID: out.ID, ChannelID: out.ChannelID, Truncated: truncated}, nil
}

func (s *Service) credentials(ctx context.Context, name string) (id, token string, err error) {
	ws, err := s.Webhooks(ctx)
	if err != nil {
		return "", "", err
	}
	if !slices.ContainsFunc(ws, func(w Webhook) bool { return w.Name == name }) {
		return "", "", fmt.Errorf("%w (%q)", ErrNoWebhook, name)
	}
	v, err := s.secrets.Get(ctx, secretKey(name))
	if err != nil {
		return "", "", fmt.Errorf("discord: the webhook's link can't be read from %s: add it again on Connections (%w)", s.secrets.Label(), err)
	}
	id, token, ok := strings.Cut(v.Reveal(), "/")
	if !ok {
		return "", "", errors.New("discord: the saved link is damaged: add the webhook again on Connections")
	}
	return id, token, nil
}

// do calls a webhook. The token is in the path, so errors never include
// the URL.
func (s *Service) do(ctx context.Context, method, id, token, query string, body, out any) error {
	var raw []byte
	if body != nil {
		var err error
		if raw, err = json.Marshal(body); err != nil {
			return err
		}
	}
	u := s.base + "/api/v10/webhooks/" + id + "/" + token
	if query != "" {
		u += "?" + query
	}
	for try := 0; ; try++ {
		req, err := http.NewRequestWithContext(ctx, method, u, bytes.NewReader(raw))
		if err != nil {
			return errors.New("discord: a bad webhook address")
		}
		if body != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		req.Header.Set("User-Agent", "DiscordBot (https://streamgremlin.com, 1) StreamGremlin")
		resp, err := s.http.Do(req)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if method == "GET" {
				return errors.New("discord: Discord didn't answer: check your connection and try again")
			}
			return ErrOutcomeUnknown
		}
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		_ = resp.Body.Close()
		var e struct {
			Message    string  `json:"message"`
			Code       int     `json:"code"`
			RetryAfter float64 `json:"retry_after"`
		}
		_ = json.Unmarshal(data, &e)
		switch {
		case resp.StatusCode == http.StatusTooManyRequests && try < 2:
			wait := time.Duration(e.RetryAfter * float64(time.Second))
			if h, err := strconv.ParseFloat(resp.Header.Get("Retry-After"), 64); err == nil && wait == 0 {
				wait = time.Duration(h * float64(time.Second))
			}
			if wait > s.maxWait {
				return fmt.Errorf("%w: Discord asks to wait %s before posting again", ErrRefused, wait.Round(time.Second))
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(wait):
			}
			continue
		case resp.StatusCode == http.StatusTooManyRequests:
			return fmt.Errorf("%w: Discord says too many posts; try again in a moment", ErrRefused)
		case resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusUnauthorized || e.Code == 10015 || e.Code == 50027:
			return ErrWebhookGone
		case resp.StatusCode >= 500:
			if method == "GET" {
				return errors.New("discord: Discord had a problem: try again in a moment")
			}
			return ErrOutcomeUnknown
		case resp.StatusCode >= 400:
			if e.Message == "" {
				e.Message = resp.Status
			}
			return fmt.Errorf("%w: %s", ErrRefused, e.Message)
		}
		if out != nil && len(data) > 0 {
			if err := json.Unmarshal(data, out); err != nil {
				return errors.New("discord: an answer that isn't JSON")
			}
		}
		return nil
	}
}
