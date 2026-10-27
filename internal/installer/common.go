package installer

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

const RecoveryAPI = 2
const MiB = 1024 * 1024
const RAMReserve = 40 * MiB
const StageName = ".onekvm-installer"
const MarkerName = "onekvm-install.json"

type Progress func(done, total int64)

func Run(name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("%s: %w: %s", name, err, strings.TrimSpace(string(out)))
	}
	return strings.TrimSpace(string(out)), nil
}

func SyncDir(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return f.Sync()
}

func WritePrivate(path string, data []byte) error {
	f, err := os.OpenFile(path+".tmp", os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	defer os.Remove(path + ".tmp")
	_, err = f.Write(data)
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	if err = os.Rename(path+".tmp", path); err != nil {
		return err
	}
	return SyncDir(filepath.Dir(path))
}

// RewriteBootPayload runs only after the image write has been verified, while
// recovery owns the unbooted target. Reuse its allocated FAT clusters: compact
// carriers do not have room for a second full kernel/initramfs temporary file.
// A failure stops recovery; keep power connected until write/readback finishes.
func RewriteBootPayload(path string, data []byte) error {
	if len(data) == 0 {
		return fmt.Errorf("empty boot payload")
	}
	if info, err := os.Lstat(path); err == nil {
		if !info.Mode().IsRegular() {
			return fmt.Errorf("boot payload is not a regular file")
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE, 0600)
	if err != nil {
		return err
	}
	n, err := f.Write(data)
	if err == nil && n != len(data) {
		err = io.ErrShortWrite
	}
	if err == nil {
		err = f.Truncate(int64(len(data)))
	}
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	actual, err := HashFile(path, nil)
	want := sha256.Sum256(data)
	if err != nil {
		return err
	}
	if actual != hex.EncodeToString(want[:]) {
		return fmt.Errorf("boot payload readback mismatch")
	}
	return SyncDir(filepath.Dir(path))
}

func JsonBytes(v any) ([]byte, error) {
	b, err := json.MarshalIndent(v, "", "  ")
	return append(b, '\n'), err
}

func WriteJSON(path string, v any) error {
	b, err := JsonBytes(v)
	if err != nil {
		return err
	}
	return WritePrivate(path, b)
}

func ReadJSON(path string, v any) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, 65537))
	if err != nil {
		return err
	}
	if len(b) > 65536 {
		return fmt.Errorf("oversized installation request")
	}
	d := json.NewDecoder(strings.NewReader(string(b)))
	d.DisallowUnknownFields()
	return d.Decode(v)
}

func HashFile(path string, progress Progress) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	s, err := f.Stat()
	if err != nil {
		return "", err
	}
	h := sha256.New()
	_, err = CopyProgress(h, f, s.Size(), progress)
	return hex.EncodeToString(h.Sum(nil)), err
}

func CopyProgress(w io.Writer, r io.Reader, total int64, progress Progress) (int64, error) {
	b := make([]byte, MiB)
	var done int64
	for {
		n, err := r.Read(b)
		if n > 0 {
			m, e := w.Write(b[:n])
			done += int64(m)
			if e != nil {
				return done, e
			}
			if m != n {
				return done, io.ErrShortWrite
			}
			if progress != nil {
				progress(done, total)
			}
		}
		if err == io.EOF {
			return done, nil
		}
		if err != nil {
			return done, err
		}
	}
}

func Memory(field string) (int64, error) {
	b, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return 0, err
	}
	for _, line := range strings.Split(string(b), "\n") {
		v := strings.Fields(line)
		if len(v) >= 2 && v[0] == field+":" {
			n, err := strconv.ParseInt(v[1], 10, 64)
			return n * 1024, err
		}
	}
	return 0, fmt.Errorf("missing %s in meminfo", field)
}
