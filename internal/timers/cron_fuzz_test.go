package timers

import (
	"testing"
	"time"
)

// FuzzParseCron: a schedule is typed by the streamer or imported. An
// accepted one gives a next run that's later, on a whole minute, and
// matches again from just before it.
func FuzzParseCron(f *testing.F) {
	for _, s := range []string{"*/15 * * * *", "0 20 * * mon-fri", "@daily", "0 0 31 2 *", "1-5/2 */3 1,15 jan-mar 0,7", "* * * * *", "60 * * * *"} {
		f.Add(s)
	}
	from := time.Date(2026, 11, 15, 12, 34, 56, 0, time.UTC)
	f.Fuzz(func(t *testing.T, s string) {
		c, err := ParseCron(s)
		if err != nil {
			return
		}
		next := c.Next(from)
		if next.IsZero() {
			return // nothing within five years, like 30 February
		}
		if !next.After(from) || next.Second() != 0 || next.Nanosecond() != 0 {
			t.Fatalf("%q: next %v from %v", s, next, from)
		}
		if again := c.Next(next.Add(-time.Second)); !again.Equal(next) {
			t.Fatalf("%q: next from just before %v is %v", s, next, again)
		}
	})
}
