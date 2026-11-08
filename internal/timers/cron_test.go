package timers

import (
	"testing"
	"time"
)

func TestCronNext(t *testing.T) {
	loc := time.UTC
	at := func(s string) time.Time {
		v, err := time.ParseInLocation("2006-01-02 15:04", s, loc)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	from := at("2026-11-08 10:07") // a Sunday
	for _, c := range []struct{ expr, want string }{
		{"* * * * *", "2026-11-08 10:08"},
		{"*/15 * * * *", "2026-11-08 10:15"},
		{"5/15 * * * *", "2026-11-08 10:20"},
		{"0 20 * * *", "2026-11-08 20:00"},
		{"0 9 * * mon-fri", "2026-11-09 09:00"},
		{"30 18 * * 1,3,5", "2026-11-09 18:30"},
		{"0 0 * * 7", "2026-11-15 00:00"},
		{"0 0 * * sun", "2026-11-15 00:00"},
		{"@hourly", "2026-11-08 11:00"},
		{"@daily", "2026-11-09 00:00"},
		{"@weekly", "2026-11-15 00:00"},
		{"@monthly", "2026-12-01 00:00"},
		{"@yearly", "2027-01-01 00:00"},
		{"0 12 1 jan *", "2027-01-01 12:00"},
		{"0 0 29 2 *", "2028-02-29 00:00"},
		{"0 0 13 * fri", "2026-11-13 00:00"}, // either day field matches: the 13th (a Friday) or the next Friday
		{"0 0 10 * mon", "2026-11-09 00:00"}, // Monday the 9th comes before the 10th
		{"7 10 * * *", "2026-11-09 10:07"},   // the current minute has passed
	} {
		c0, err := ParseCron(c.expr)
		if err != nil {
			t.Errorf("%s: %v", c.expr, err)
			continue
		}
		if got := c0.Next(from); !got.Equal(at(c.want)) {
			t.Errorf("%s: %s, want %s", c.expr, got.Format("2006-01-02 15:04 Mon"), c.want)
		}
	}
}

func TestCronThatNeverMatches(t *testing.T) {
	c, err := ParseCron("0 0 31 2 *")
	if err != nil {
		t.Fatal(err)
	}
	if got := c.Next(time.Now()); !got.IsZero() {
		t.Fatalf("31 February: %v", got)
	}
}

func TestCronRefuses(t *testing.T) {
	for _, s := range []string{"", "* * * *", "* * * * * *", "60 * * * *", "* 24 * * *", "* * 0 * *", "* * * 13 *",
		"* * * * 8", "*/0 * * * *", "5-1 * * * *", "a * * * *", "* * * foo *", "@sometimes", "1,,2 * * * *"} {
		if _, err := ParseCron(s); err == nil {
			t.Errorf("%q accepted", s)
		}
	}
}

func TestCronFollowsLocalTimeAcrossDaylightSaving(t *testing.T) {
	ny, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Skip("no time zone data")
	}
	c, _ := ParseCron("30 2 * * *")
	// 2026-03-08 02:30 doesn't exist in New York; classic cron skips it.
	got := c.Next(time.Date(2026, 3, 7, 12, 0, 0, 0, ny))
	if got.Hour() != 2 && got.Hour() != 3 {
		t.Fatalf("%v", got)
	}
	if !got.After(time.Date(2026, 3, 7, 12, 0, 0, 0, ny)) {
		t.Fatalf("%v", got)
	}
}
