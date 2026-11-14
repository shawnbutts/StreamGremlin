package banlists

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"
)

// Reading a ban list file (docs/adr/0049-ban-lists.md): one Twitch login
// or user id a line, or a CSV (comma, semicolon or tab) whose column is
// named login, user_login, username, user_name, name, user, id or user_id
// (the first column otherwise). Blank lines and lines starting with # are
// skipped, a leading @ and surrounding quotes are dropped, and names are
// kept in lower case. Anything else isn't a Twitch name and is counted as
// skipped, never sent anywhere.

// MaxLines is the most lines a file may have.
const MaxLines = 5_000_000

// maxLine is the longest line read.
const maxLine = 64 << 10

// ErrTooLong means the file has more than MaxLines lines.
var ErrTooLong = fmt.Errorf("banlists: a ban list is up to %d lines", MaxLines)

var (
	loginPattern = regexp.MustCompile(`^[a-z0-9_]{1,25}$`)
	idPattern    = regexp.MustCompile(`^[0-9]{1,20}$`)
)

// Name is one account named in a file: a login, or id:<user id>.
type Name string

// IsID reports whether the name is a user id.
func (n Name) IsID() bool { return strings.HasPrefix(string(n), "id:") }

// Value is the login or the id without its prefix.
func (n Name) Value() string { return strings.TrimPrefix(string(n), "id:") }

// ParseName reads one value as a Twitch login or user id.
func ParseName(v string) (Name, bool) {
	v = strings.ToLower(strings.Trim(strings.TrimSpace(v), `"'`))
	v = strings.TrimPrefix(strings.TrimSpace(v), "@")
	switch {
	case idPattern.MatchString(v):
		return Name("id:" + v), true
	case loginPattern.MatchString(v):
		return Name(v), true
	}
	return "", false
}

var columns = map[string]int{"login": 0, "user_login": 0, "username": 0, "user_name": 1, "name": 2, "user": 2, "user_id": 3, "id": 3}

func splitRow(line string) []string {
	for _, sep := range []string{"\t", ",", ";"} {
		if strings.Contains(line, sep) {
			return strings.Split(line, sep)
		}
	}
	return []string{line}
}

// Read calls each for every name in r, in order, and returns how many
// lines weren't a name. A line that's too long, or too many lines, stops it.
func Read(r io.Reader, each func(Name) error) (skipped int, err error) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 4096), maxLine)
	col, lines, first := 0, 0, true
	for sc.Scan() {
		lines++
		if lines > MaxLines {
			return skipped, ErrTooLong
		}
		line := strings.TrimSpace(sc.Text())
		if first {
			line = strings.TrimPrefix(line, "\ufeff")
		}
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		row := splitRow(line)
		if first {
			first = false
			// A header names the column to read.
			best := -1
			for i, h := range row {
				h = strings.ToLower(strings.Trim(strings.TrimSpace(h), `"'`))
				if rank, ok := columns[h]; ok && (best < 0 || rank < columns[strings.ToLower(strings.Trim(strings.TrimSpace(row[best]), `"'`))]) {
					best = i
				}
			}
			if best >= 0 {
				col = best
				continue
			}
		}
		if col >= len(row) {
			skipped++
			continue
		}
		n, ok := ParseName(row[col])
		if !ok {
			skipped++
			continue
		}
		if err := each(n); err != nil {
			return skipped, err
		}
	}
	if err := sc.Err(); err != nil {
		if errors.Is(err, bufio.ErrTooLong) {
			return skipped, fmt.Errorf("banlists: line %d is longer than %d bytes: is this a ban list?", lines+1, maxLine)
		}
		return skipped, err
	}
	return skipped, nil
}
