package bootstrap

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/WhitenWhiten/topophany/internal/secure"
)

// A backup is authenticated by a key in the installation's private backup
// tree. Its location is derived from the selected path, never from metadata
// supplied by that backup. Legacy unsigned backups are deliberately refused.
func rollbackKey(ctx context.Context, install string, create bool) ([]byte, error) {
	backups := filepath.Join(install, ".backups")
	if err := trustedRollbackPath(ctx, backups, true); err != nil {
		return nil, err
	}
	path := filepath.Join(backups, "rollback.key")
	if _, err := os.Lstat(path); create && os.IsNotExist(err) {
		key := make([]byte, 32)
		if _, err = rand.Read(key); err != nil {
			return nil, err
		}
		if err = secure.AtomicWrite(path, key, 0600); err != nil {
			return nil, err
		}
	}
	key, err := readRollbackFile(path, 32)
	if err != nil || len(key) != 32 {
		return nil, errors.New("missing or invalid private rollback key")
	}
	return key, nil
}

func signRollback(r installation, dir string, key []byte) (string, error) {
	path, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}
	r.Signature = ""
	raw, err := json.Marshal(r)
	if err != nil {
		return "", err
	}
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte("topophany-rollback-v1\x00" + path + "\x00"))
	mac.Write(raw)
	return hex.EncodeToString(mac.Sum(nil)), nil
}

// All bytes are read, authenticated and retained before any service or target
// is touched. Rollback never reopens payloads after validation.
func loadRollback(ctx context.Context, dir string) (installation, map[string][]byte, error) {
	var r installation
	path, err := filepath.Abs(dir)
	if err != nil {
		return r, nil, err
	}
	backups := filepath.Dir(path)
	install := filepath.Dir(backups)
	if filepath.Base(backups) != ".backups" || !strings.HasPrefix(filepath.Base(path), "install-") || filepath.Base(path) == "install-" {
		return r, nil, errors.New("rollback must select an installer-created .backups/install-* directory")
	}
	if err = trustedRollbackPath(ctx, path, true); err != nil {
		return r, nil, err
	}
	key, err := rollbackKey(ctx, install, false)
	if err != nil {
		return r, nil, err
	}
	raw, err := readRollbackFile(filepath.Join(path, "rollback.json"), 1<<20)
	if err != nil {
		return r, nil, err
	}
	if err = secure.Decode(bytes.NewReader(raw), &r, 1<<20); err != nil {
		return r, nil, err
	}
	expected, err := signRollback(r, path, key)
	if err != nil || !hmac.Equal([]byte(r.Signature), []byte(expected)) {
		return r, nil, errors.New("rollback record authentication failed; unsigned legacy backups cannot be restored")
	}
	manifestInstall, err := filepath.Abs(r.Manifest.InstallDir)
	if err != nil || manifestInstall != install {
		return r, nil, errors.New("rollback belongs to a different installation")
	}
	if err = validateRollback(r); err != nil {
		return r, nil, err
	}
	payloads := map[string][]byte{}
	for _, f := range r.Files {
		if f.Saved == "" {
			continue
		}
		b, err := readRollbackFile(filepath.Join(path, f.Saved), 64<<20)
		if err != nil {
			return r, nil, err
		}
		if secure.Hash(string(b)) != f.SHA256 {
			return r, nil, errors.New("rollback payload checksum mismatch")
		}
		payloads[f.Saved] = b
	}
	return r, payloads, nil
}

func readRollbackFile(path string, limit int64) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || runtime.GOOS != "windows" && info.Mode().Perm()&0077 != 0 || info.Size() > limit {
		return nil, errors.New("rollback requires private regular files within size limits")
	}
	if err = trustedRollbackOwner(info); err != nil {
		return nil, err
	}
	return secure.ReadBounded(path, limit)
}
