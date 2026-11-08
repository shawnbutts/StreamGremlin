package secrets

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sync"
)

// File names in the data folder.
const (
	FileName    = "secrets.enc"
	KeyFileName = "secrets.key"
)

const (
	fileFormat  = "streamgremlin-secrets"
	fileVersion = 1
	keyBytes    = 32
)

// ErrDamaged means the encrypted file or its key can't be used: wrong key,
// changed bytes, a missing key file, or a file from another installation.
var ErrDamaged = errors.New("secrets: the encrypted secrets file can't be read")

// fileBackend keeps every secret in one AES-256-GCM file. The key is a
// separate file, so a copy of the database or of secrets.enc alone is
// useless. The installation id is authenticated, so a file copied from
// another data folder doesn't decrypt.
type fileBackend struct {
	path, keyPath string
	aad           []byte
	mu            sync.Mutex
}

type envelope struct {
	Format  string `json:"format"`
	Version int    `json:"version"`
	Nonce   []byte `json:"nonce"`
	Data    []byte `json:"data"`
}

func newFileBackend(dir, installationID string) *fileBackend {
	return &fileBackend{
		path:    filepath.Join(dir, FileName),
		keyPath: filepath.Join(dir, KeyFileName),
		aad:     []byte(fileFormat + "/v1/" + installationID),
	}
}

func (f *fileBackend) get(_ context.Context, key string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	m, err := f.load(false)
	if err != nil {
		return "", err
	}
	v, ok := m[key]
	if !ok {
		return "", ErrNotFound
	}
	return v, nil
}

func (f *fileBackend) set(_ context.Context, key, value string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	m, err := f.load(true)
	if err != nil {
		return err
	}
	m[key] = value
	return f.save(m)
}

func (f *fileBackend) del(_ context.Context, key string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	m, err := f.load(false)
	if err != nil {
		return err
	}
	if _, ok := m[key]; !ok {
		return nil
	}
	delete(m, key)
	return f.save(m)
}

// load decrypts the file. A missing file is an empty store; createKey makes
// the key file if neither file exists yet.
func (f *fileBackend) load(createKey bool) (map[string]string, error) {
	raw, err := readPrivate(f.path)
	if errors.Is(err, fs.ErrNotExist) {
		if createKey {
			if err := f.ensureKey(); err != nil {
				return nil, err
			}
		}
		return map[string]string{}, nil
	}
	if err != nil {
		return nil, err
	}
	aead, err := f.cipher()
	if err != nil {
		return nil, err
	}
	var env envelope
	if err := json.Unmarshal(raw, &env); err != nil || env.Format != fileFormat {
		return nil, fmt.Errorf("%w: %s isn't a secrets file", ErrDamaged, f.path)
	}
	if env.Version != fileVersion {
		return nil, fmt.Errorf("%w: %s has format version %d; this build reads %d", ErrDamaged, f.path, env.Version, fileVersion)
	}
	if len(env.Nonce) != aead.NonceSize() {
		return nil, fmt.Errorf("%w: %s is damaged", ErrDamaged, f.path)
	}
	plain, err := aead.Open(nil, env.Nonce, env.Data, f.aad)
	if err != nil {
		return nil, fmt.Errorf("%w: %s doesn't match its key %s, was changed, or comes from another data folder",
			ErrDamaged, f.path, f.keyPath)
	}
	m := map[string]string{}
	if err := json.Unmarshal(plain, &m); err != nil {
		return nil, fmt.Errorf("%w: %s is damaged", ErrDamaged, f.path)
	}
	return m, nil
}

func (f *fileBackend) save(m map[string]string) error {
	aead, err := f.cipher()
	if err != nil {
		return err
	}
	plain, err := json.Marshal(m)
	if err != nil {
		return err
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return err
	}
	out, err := json.Marshal(envelope{
		Format:  fileFormat,
		Version: fileVersion,
		Nonce:   nonce,
		Data:    aead.Seal(nil, nonce, plain, f.aad),
	})
	if err != nil {
		return err
	}
	return writeAtomic(f.path, out)
}

func (f *fileBackend) cipher() (cipher.AEAD, error) {
	key, err := readPrivate(f.keyPath)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("%w: its key file %s is missing", ErrDamaged, f.keyPath)
	}
	if err != nil {
		return nil, err
	}
	if len(key) != keyBytes {
		return nil, fmt.Errorf("%w: its key file %s is damaged", ErrDamaged, f.keyPath)
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

// ensureKey creates the key file, never replacing an existing one.
func (f *fileBackend) ensureKey() error {
	key := make([]byte, keyBytes)
	if _, err := rand.Read(key); err != nil {
		return err
	}
	kf, err := os.OpenFile(f.keyPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if errors.Is(err, fs.ErrExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("secrets: create %s: %w", f.keyPath, err)
	}
	if _, err := kf.Write(key); err != nil {
		_ = kf.Close()
		return err
	}
	if err := kf.Sync(); err != nil {
		_ = kf.Close()
		return err
	}
	if err := kf.Close(); err != nil {
		return err
	}
	return syncDir(filepath.Dir(f.keyPath))
}

// readPrivate reads a file, refusing it if other users can read it (as ssh
// does with private keys).
func readPrivate(path string) ([]byte, error) {
	if runtime.GOOS != "windows" {
		fi, err := os.Stat(path)
		if err != nil {
			return nil, err
		}
		if fi.Mode().Perm()&0o077 != 0 {
			return nil, fmt.Errorf("secrets: %s can be read by other users (mode %v); run `chmod 600 %s`",
				path, fi.Mode().Perm(), path)
		}
	}
	return os.ReadFile(path)
}

// writeAtomic replaces path with data: write a synced temporary file, then
// rename it over the old one, so a crash leaves the old or the new file.
func writeAtomic(path string, data []byte) error {
	tmp := path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		return err
	}
	return syncDir(filepath.Dir(path))
}

func syncDir(dir string) error {
	if runtime.GOOS == "windows" {
		return nil // directories can't be synced there; rename is durable enough
	}
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}
