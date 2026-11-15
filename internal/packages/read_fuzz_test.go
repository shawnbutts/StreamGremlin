package packages

import (
	"archive/zip"
	"bytes"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"
)

// FuzzReadZip: a package is a ZIP from anyone. An accepted one has only
// clean relative paths (nothing can be written outside its folder), a hash
// for every file, and a size that adds up.
func FuzzReadZip(f *testing.F) {
	f.Add(zipFolder(f, filepath.Join("..", "..", "examples", "plugins", "dice-tray"), ""))
	f.Add(zipFolder(f, filepath.Join("..", "..", "examples", "plugins", "dice-tray"), "dice-tray/"))
	f.Add([]byte("PK\x05\x06" + strings.Repeat("\x00", 18)))
	f.Fuzz(func(t *testing.T, data []byte) {
		r, err := ReadZip(data)
		if err != nil {
			return
		}
		var size int64
		for p, b := range r.Files {
			if p == "" || !utf8.ValidString(p) || strings.HasPrefix(p, "/") || strings.Contains(p, `\`) || path.Clean(p) != p ||
				p == ".." || strings.HasPrefix(p, "../") || filepath.IsAbs(p) || strings.Contains(p, ":") {
				t.Fatalf("accepted the path %q", p)
			}
			if r.Hashes[p] == "" {
				t.Fatalf("no hash for %q", p)
			}
			size += int64(len(b))
		}
		if size != r.Size || len(r.Hashes) != len(r.Files) || r.Digest == "" {
			t.Fatalf("size %d, want %d; %d hashes for %d files", r.Size, size, len(r.Hashes), len(r.Files))
		}
	})
}

// zipFolder zips a folder, each name under prefix.
func zipFolder(f *testing.F, dir, prefix string) []byte {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(dir, p)
		b, err := os.ReadFile(p) //nolint:gosec // our own examples
		if err != nil {
			return err
		}
		w, err := zw.Create(prefix + filepath.ToSlash(rel))
		if err != nil {
			return err
		}
		_, err = w.Write(b)
		return err
	})
	if err != nil {
		f.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		f.Fatal(err)
	}
	return buf.Bytes()
}
