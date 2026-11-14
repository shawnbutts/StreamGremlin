package banlists

import (
	"errors"
	"strings"
	"testing"
)

func readAll(t *testing.T, s string) ([]Name, int, error) {
	t.Helper()
	var out []Name
	skipped, err := Read(strings.NewReader(s), func(n Name) error { out = append(out, n); return nil })
	return out, skipped, err
}

func TestReadLines(t *testing.T) {
	got, skipped, err := readAll(t, "\ufeff# my list\nSpamBot1\n@other_bot\n\n  \"quoted\"  \n123456\nnot a name!\nhttps://twitch.tv/x\n")
	if err != nil || skipped != 2 || len(got) != 4 || got[0] != "spambot1" || got[1] != "other_bot" || got[2] != "quoted" || got[3] != "id:123456" {
		t.Fatalf("%v %d %v", got, skipped, err)
	}
	if !got[3].IsID() || got[3].Value() != "123456" || got[0].IsID() {
		t.Fatal("id")
	}
}

func TestReadCSV(t *testing.T) {
	// A header picks the login column, wherever it is.
	got, skipped, err := readAll(t, "user_id,user_login,reason\n1,alpha,spam\n2,beta,\"hate\"\n3,,x\n")
	if err != nil || skipped != 1 || len(got) != 2 || got[0] != "alpha" || got[1] != "beta" {
		t.Fatalf("%v %d %v", got, skipped, err)
	}
	// Only ids: the id column.
	got, _, _ = readAll(t, "id;note\n555;a\n666;b\n")
	if len(got) != 2 || got[0] != "id:555" {
		t.Fatalf("%v", got)
	}
	// No header: the first column.
	got, _, _ = readAll(t, "gamma\tx\ndelta\ty\n")
	if len(got) != 2 || got[0] != "gamma" || got[1] != "delta" {
		t.Fatalf("%v", got)
	}
}

func TestReadLimits(t *testing.T) {
	if _, _, err := readAll(t, strings.Repeat("a", 70<<10)); err == nil || !strings.Contains(err.Error(), "longer than") {
		t.Fatal(err)
	}
	stop := errors.New("stop")
	if _, err := Read(strings.NewReader("a\nb\n"), func(Name) error { return stop }); !errors.Is(err, stop) {
		t.Fatal(err)
	}
	if n, ok := ParseName(strings.Repeat("a", 26)); ok {
		t.Fatal(n)
	}
}
