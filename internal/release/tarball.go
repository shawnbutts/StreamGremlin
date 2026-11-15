package release

import (
	"archive/tar"
	"compress/gzip"
	"io"
	"io/fs"
	"os"
	"path"
	"sort"
	"time"
)

// Tarball writes the files under root as a .tar.gz, each under prefix/,
// the same bytes every time for the same files (D-114): names in order,
// one modification time, owner root, and modes only 0755 (directories
// and programs) or 0644. Symbolic links and other special files are
// refused.
func Tarball(w io.Writer, root fs.FS, prefix string, mtime time.Time) error {
	var paths []string
	err := fs.WalkDir(root, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if p != "." {
			paths = append(paths, p)
		}
		return nil
	})
	if err != nil {
		return err
	}
	sort.Strings(paths)
	gz, err := gzip.NewWriterLevel(w, gzip.BestCompression)
	if err != nil {
		return err
	}
	tw := tar.NewWriter(gz)
	mtime = mtime.UTC().Truncate(time.Second)
	head := func(name string, typ byte, mode int64, size int64) *tar.Header {
		return &tar.Header{Name: name, Typeflag: typ, Mode: mode, Size: size, ModTime: mtime, Uname: "root", Gname: "root", Format: tar.FormatPAX}
	}
	if err := tw.WriteHeader(head(prefix+"/", tar.TypeDir, 0o755, 0)); err != nil {
		return err
	}
	for _, p := range paths {
		info, err := fs.Stat(root, p)
		if err != nil {
			return err
		}
		name := path.Join(prefix, p)
		switch {
		case info.IsDir():
			err = tw.WriteHeader(head(name+"/", tar.TypeDir, 0o755, 0))
		case info.Mode().IsRegular():
			mode := int64(0o644)
			if info.Mode()&0o111 != 0 {
				mode = 0o755
			}
			if err = tw.WriteHeader(head(name, tar.TypeReg, mode, info.Size())); err == nil {
				err = copyFile(tw, root, p)
			}
		default:
			err = &fs.PathError{Op: "tarball", Path: p, Err: fs.ErrInvalid}
		}
		if err != nil {
			return err
		}
	}
	if err := tw.Close(); err != nil {
		return err
	}
	return gz.Close()
}

func copyFile(w io.Writer, root fs.FS, p string) error {
	f, err := root.Open(p)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	_, err = io.Copy(w, f)
	return err
}

// WriteTarball writes Tarball to out.
func WriteTarball(out, root, prefix string, mtime time.Time) error {
	f, err := os.Create(out) //nolint:gosec // The path the Makefile names.
	if err != nil {
		return err
	}
	if err := Tarball(f, os.DirFS(root), prefix, mtime); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}

// Stamp dates every file and folder under dir at t, so packages made
// from them (which keep files' times) are the same bytes every time.
// It stays inside dir: links are dated, never followed.
func Stamp(dir string, t time.Time) error {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return err
	}
	defer func() { _ = root.Close() }()
	return fs.WalkDir(root.FS(), ".", func(p string, _ fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		return root.Chtimes(p, t, t)
	})
}
