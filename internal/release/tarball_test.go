package release

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"io"
	"os"
	"path/filepath"
	"testing"
	"testing/fstest"
	"time"
)

// The same files make the same bytes, whatever their times and owners,
// with the modes a package expects.
func TestTarball(t *testing.T) {
	when := time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)
	files := func(touched time.Time) fstest.MapFS {
		return fstest.MapFS{
			"streamgremlin":     {Data: []byte("cli"), Mode: 0o700, ModTime: touched},
			"doc/LICENSE":       {Data: []byte("licence"), Mode: 0o600, ModTime: touched},
			"icons/16x16/a.png": {Data: []byte("png"), Mode: 0o664, ModTime: touched},
			"app.desktop":       {Data: []byte("[Desktop Entry]"), Mode: 0o644, ModTime: touched},
		}
	}
	var a, b bytes.Buffer
	if err := Tarball(&a, files(time.Now()), "StreamGremlin-1.0.0-linux-x64", when); err != nil {
		t.Fatal(err)
	}
	if err := Tarball(&b, files(time.Unix(0, 0)), "StreamGremlin-1.0.0-linux-x64", when); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(a.Bytes(), b.Bytes()) {
		t.Fatal("the same files made different archives")
	}
	gz, err := gzip.NewReader(&a)
	if err != nil {
		t.Fatal(err)
	}
	tr := tar.NewReader(gz)
	var got []string
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if !h.ModTime.Equal(when) || h.Uid != 0 || h.Uname != "root" {
			t.Errorf("%s: %v %d %s", h.Name, h.ModTime, h.Uid, h.Uname)
		}
		got = append(got, h.Name+" "+map[int64]string{0o755: "755", 0o644: "644"}[h.Mode])
	}
	want := []string{
		"StreamGremlin-1.0.0-linux-x64/ 755",
		"StreamGremlin-1.0.0-linux-x64/app.desktop 644",
		"StreamGremlin-1.0.0-linux-x64/doc/ 755",
		"StreamGremlin-1.0.0-linux-x64/doc/LICENSE 644",
		"StreamGremlin-1.0.0-linux-x64/icons/ 755",
		"StreamGremlin-1.0.0-linux-x64/icons/16x16/ 755",
		"StreamGremlin-1.0.0-linux-x64/icons/16x16/a.png 644",
		"StreamGremlin-1.0.0-linux-x64/streamgremlin 755",
	}
	if len(got) != len(want) {
		t.Fatalf("got %q", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("%d: got %q, want %q", i, got[i], want[i])
		}
	}
}

func TestStamp(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "a", "b"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "a", "b", "f"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	when := time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)
	if err := Stamp(dir, when); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"a", "a/b", "a/b/f"} {
		st, err := os.Stat(filepath.Join(dir, p))
		if err != nil || !st.ModTime().Equal(when) {
			t.Errorf("%s: %v %v", p, st.ModTime(), err)
		}
	}
}
