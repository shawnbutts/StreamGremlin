//go:build acceptance

package acceptance

import (
	"strings"
	"testing"
)

// Back up, change something, restore at the next start, and find it as it
// was, with what the restore replaced kept as a backup.
func TestBackupAndRestoreThroughTheCLI(t *testing.T) {
	dir := t.TempDir()
	e := startEngine(t, dir)
	if out, errOut, code := cli(t, dir, "vars", "set", "global", "deaths", "1"); code != 0 {
		t.Fatal(out, errOut)
	}
	out, errOut, code := cli(t, dir, "backups", "create")
	if code != 0 || !strings.HasPrefix(out, "Backed up to streamgremlin-manual-") {
		t.Fatal(out, errOut)
	}
	name := strings.TrimSpace(strings.TrimPrefix(out, "Backed up to "))
	if out, _, code := cli(t, dir, "backups", "verify", name); code != 0 || !strings.Contains(out, "It's good") {
		t.Fatal(out)
	}
	cli(t, dir, "vars", "set", "global", "deaths", "2")
	if out, _, code := cli(t, dir, "backups", "restore", name); code != 0 || !strings.Contains(out, "Restart StreamGremlin") {
		t.Fatal(out)
	}
	// Nothing changes until the restart.
	if out, _, _ := cli(t, dir, "vars", "global"); !strings.Contains(out, "2") {
		t.Fatalf("changed before the restart:\n%s", out)
	}
	if out, _, _ := cli(t, dir, "backups"); !strings.Contains(out, "(restored at the next start)") {
		t.Fatal(out)
	}
	e.kill()
	startEngine(t, dir)
	if out, _, _ := cli(t, dir, "vars", "global"); !strings.Contains(out, "deaths") || !strings.Contains(out, " 1") {
		t.Fatalf("not restored:\n%s", out)
	}
	out, _, _ = cli(t, dir, "backups")
	if !strings.Contains(out, "pre-restore") || !strings.Contains(out, "Last restore:") || strings.Contains(out, "(restored at the next start)") {
		t.Fatalf("backups:\n%s", out)
	}
	if _, errOut, code := cli(t, dir, "backups", "restore", "../streamgremlin.db"); code == 0 || !strings.Contains(errOut, "no such backup") {
		t.Fatalf("%d %s", code, errOut)
	}
}
