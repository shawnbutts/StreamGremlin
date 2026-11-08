// Package commands keeps one answer per chat command
// (docs/adr/0011-command-ownership.md). A claim says that something
// answers a !command, for which chats and which chatters; two claims on
// one command conflict unless their chats or their chatters never meet.
// The rules are plain functions; the workflow engine applies them.
package commands

import (
	"fmt"
	"slices"
	"sort"
	"strings"
)

// Claim is one owner answering one command.
type Claim struct {
	Command string `json:"command"` // lowercase, without "!"
	Owner   string `json:"owner"`   // a workflow id
	Name    string `json:"name"`    // the owner's name, for messages
	// Sources limits it to some chats ("twitch"); nil means every chat.
	Sources []string `json:"sources,omitempty"`
	// Roles limits it to some chatters' roles; nil means everyone.
	Roles []string `json:"roles,omitempty"`
}

// meet reports whether two limits can both hold: nil is "any".
func meet(a, b []string) bool {
	if a == nil || b == nil {
		return true
	}
	for _, x := range a {
		if slices.Contains(b, x) {
			return true
		}
	}
	return false
}

// Overlap reports whether two claims could both answer one message.
func Overlap(a, b Claim) bool {
	return a.Command == b.Command && a.Owner != b.Owner && meet(a.Sources, b.Sources) && meet(a.Roles, b.Roles)
}

// Conflict is a new claim that overlaps one already held.
type Conflict struct {
	Command string `json:"command"`
	Owner   string `json:"owner"` // who holds it now
	Name    string `json:"name"`
}

// Conflicts returns, for each new claim, the held claims it overlaps, by
// command then owner, each once.
func Conflicts(add, held []Claim) []Conflict {
	seen := map[string]bool{}
	var out []Conflict
	for _, a := range add {
		for _, h := range held {
			if !Overlap(a, h) || seen[h.Command+"\x00"+h.Owner] {
				continue
			}
			seen[h.Command+"\x00"+h.Owner] = true
			out = append(out, Conflict{Command: h.Command, Owner: h.Owner, Name: h.Name})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Command != out[j].Command {
			return out[i].Command < out[j].Command
		}
		return out[i].Owner < out[j].Owner
	})
	return out
}

// TakenError refuses a change that would answer a command something else
// already answers.
type TakenError struct{ Conflicts []Conflict }

func (e *TakenError) Error() string {
	parts := make([]string, len(e.Conflicts))
	for i, c := range e.Conflicts {
		parts[i] = fmt.Sprintf("!%s is already answered by “%s”", c.Command, c.Name)
	}
	return strings.Join(parts, "; ") +
		". Turn that off first, or limit who can use each so they never overlap: one answer per command."
}

// Owner is one workflow that answers a command, on or off.
type Owner struct {
	Owner   string   `json:"owner"`
	Name    string   `json:"name"`
	Enabled bool     `json:"enabled"`
	Sources []string `json:"sources,omitempty"`
	Roles   []string `json:"roles,omitempty"`
}

// Entry is one command and everything that answers it.
type Entry struct {
	Command string  `json:"command"`
	Owners  []Owner `json:"owners"`
	// Conflict: two owners that are on overlap. Left from before the check,
	// or edited outside StreamGremlin; fixed by turning one off.
	Conflict bool `json:"conflict"`
}

// Table lists every command claimed, by command, with its owners (those on
// first) and whether owners that are on overlap.
func Table(claims []Claim, enabled map[string]bool) []Entry {
	by := map[string][]Claim{}
	for _, c := range claims {
		by[c.Command] = append(by[c.Command], c)
	}
	out := make([]Entry, 0, len(by))
	for cmd, cs := range by {
		e := Entry{Command: cmd}
		seen := map[string]bool{}
		for i, c := range cs {
			if !seen[c.Owner] {
				seen[c.Owner] = true
				e.Owners = append(e.Owners, Owner{Owner: c.Owner, Name: c.Name, Enabled: enabled[c.Owner], Sources: c.Sources, Roles: c.Roles})
			}
			for _, d := range cs[i+1:] {
				if enabled[c.Owner] && enabled[d.Owner] && Overlap(c, d) {
					e.Conflict = true
				}
			}
		}
		sort.SliceStable(e.Owners, func(i, j int) bool {
			if e.Owners[i].Enabled != e.Owners[j].Enabled {
				return e.Owners[i].Enabled
			}
			return e.Owners[i].Name < e.Owners[j].Name
		})
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Command < out[j].Command })
	return out
}
