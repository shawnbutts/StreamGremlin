package timers

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Cron is a parsed five-field schedule: minute, hour, day of month, month
// and day of week, in the computer's local time (docs/adr/0013-timers.md).
type Cron struct {
	min, hour, dom, month, dow uint64 // bit i set: value i matches
	domAny, dowAny             bool   // the field was "*"
}

var cronShorthands = map[string]string{
	"@hourly":   "0 * * * *",
	"@daily":    "0 0 * * *",
	"@midnight": "0 0 * * *",
	"@weekly":   "0 0 * * 0",
	"@monthly":  "0 0 1 * *",
	"@yearly":   "0 0 1 1 *",
	"@annually": "0 0 1 1 *",
}

var (
	monthNames = map[string]int{"jan": 1, "feb": 2, "mar": 3, "apr": 4, "may": 5, "jun": 6,
		"jul": 7, "aug": 8, "sep": 9, "oct": 10, "nov": 11, "dec": 12}
	dayNames = map[string]int{"sun": 0, "mon": 1, "tue": 2, "wed": 3, "thu": 4, "fri": 5, "sat": 6}
)

// ParseCron reads a schedule like "*/15 * * * *", "0 20 * * mon-fri" or
// "@daily". 0 and 7 are both Sunday.
func ParseCron(s string) (Cron, error) {
	s = strings.TrimSpace(strings.ToLower(s))
	if full, ok := cronShorthands[s]; ok {
		s = full
	}
	f := strings.Fields(s)
	if len(f) != 5 {
		return Cron{}, errors.New("a schedule has five parts: minute, hour, day of the month, month and day of the week, like 0 20 * * *")
	}
	var c Cron
	var err error
	if c.min, err = cronField(f[0], 0, 59, nil, "minute"); err != nil {
		return Cron{}, err
	}
	if c.hour, err = cronField(f[1], 0, 23, nil, "hour"); err != nil {
		return Cron{}, err
	}
	if c.dom, err = cronField(f[2], 1, 31, nil, "day of the month"); err != nil {
		return Cron{}, err
	}
	if c.month, err = cronField(f[3], 1, 12, monthNames, "month"); err != nil {
		return Cron{}, err
	}
	if c.dow, err = cronField(f[4], 0, 7, dayNames, "day of the week"); err != nil {
		return Cron{}, err
	}
	if c.dow&(1<<7) != 0 {
		c.dow |= 1 // 7 is Sunday too
	}
	c.domAny, c.dowAny = f[2] == "*", f[4] == "*"
	return c, nil
}

func cronField(s string, lo, hi int, names map[string]int, what string) (uint64, error) {
	var bits uint64
	for part := range strings.SplitSeq(s, ",") {
		step := 1
		if base, st, ok := strings.Cut(part, "/"); ok {
			n, err := strconv.Atoi(st)
			if err != nil || n < 1 || n > hi {
				return 0, fmt.Errorf("the %s's step %q isn't a number from 1 to %d", what, st, hi)
			}
			part, step = base, n
		}
		from, to := lo, hi
		if part != "*" {
			a, b, isRange := strings.Cut(part, "-")
			var err error
			if from, err = cronValue(a, lo, hi, names, what); err != nil {
				return 0, err
			}
			to = from
			if isRange {
				if to, err = cronValue(b, lo, hi, names, what); err != nil {
					return 0, err
				}
				if to < from {
					return 0, fmt.Errorf("the %s's range %q runs backwards", what, part)
				}
			} else if step > 1 {
				to = hi // "5/15" means from 5, every 15
			}
		}
		for v := from; v <= to; v += step {
			bits |= 1 << uint(v) //nolint:gosec // v is within lo..hi, at most 59
		}
	}
	return bits, nil
}

func cronValue(s string, lo, hi int, names map[string]int, what string) (int, error) {
	if v, ok := names[s]; ok {
		return v, nil
	}
	v, err := strconv.Atoi(s)
	if err != nil || v < lo || v > hi {
		return 0, fmt.Errorf("the %s %q isn't from %d to %d", what, s, lo, hi)
	}
	return v, nil
}

func has(bits uint64, v int) bool { return bits&(1<<uint(v)) != 0 } //nolint:gosec // v is 0..59

// dayMatches follows classic cron: when both day fields are restricted,
// either may match.
func (c Cron) dayMatches(t time.Time) bool {
	dom, dow := has(c.dom, t.Day()), has(c.dow, int(t.Weekday()))
	switch {
	case c.domAny && c.dowAny:
		return true
	case c.domAny:
		return dow
	case c.dowAny:
		return dom
	}
	return dom || dow
}

// Next returns the first matching minute after t, in t's location, or the
// zero time when nothing matches within five years (like 0 0 31 2 *).
func (c Cron) Next(t time.Time) time.Time {
	t = t.Truncate(time.Minute).Add(time.Minute)
	end := t.AddDate(5, 0, 0)
	for t.Before(end) {
		switch {
		case !has(c.month, int(t.Month())):
			t = time.Date(t.Year(), t.Month()+1, 1, 0, 0, 0, 0, t.Location())
		case !c.dayMatches(t):
			t = time.Date(t.Year(), t.Month(), t.Day()+1, 0, 0, 0, 0, t.Location())
		case !has(c.hour, t.Hour()):
			t = t.Truncate(time.Hour).Add(time.Hour)
		case !has(c.min, t.Minute()):
			t = t.Add(time.Minute)
		default:
			return t
		}
	}
	return time.Time{}
}
