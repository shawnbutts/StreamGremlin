// Package chat holds what chat means on every platform, starting with
// recognising a command such as "!so @friend".
package chat

import (
	"regexp"
	"strings"
)

var commandName = regexp.MustCompile(`^[a-z0-9_]{1,32}$`)

// Command is a parsed chat command.
type Command struct {
	Name    string   // lowercase, without the "!": "so"
	Args    string   // everything after the name, trimmed: "@friend"
	ArgList []string // Args split on spaces
}

// ParseCommand reads "!name args". The name is 1 to 32 letters, digits or
// underscores, matched without regard to case. Anything else isn't a command.
func ParseCommand(text string) (Command, bool) {
	text = strings.TrimSpace(text)
	if !strings.HasPrefix(text, "!") {
		return Command{}, false
	}
	name, args, _ := strings.Cut(text[1:], " ")
	name = strings.ToLower(name)
	if !commandName.MatchString(name) {
		return Command{}, false
	}
	args = strings.TrimSpace(args)
	return Command{Name: name, Args: args, ArgList: strings.Fields(args)}, true
}
