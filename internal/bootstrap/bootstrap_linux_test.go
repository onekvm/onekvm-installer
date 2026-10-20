package bootstrap_test

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

const fixtureBinary = `#!/bin/sh
printf 'started\n' > "$ONEKVM_TEST_MARKER"
printf 'arg=<%s>\n' "$@"
exit "${ONEKVM_TEST_EXIT:-0}"
`

func archive(t *testing.T, arch string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	name := "onekvm-installer-0.1.0-linux-" + arch + "/onekvm-installer"
	if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0755, Size: int64(len(fixtureBinary))}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write([]byte(fixtureBinary)); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

type fixture struct {
	mu       sync.RWMutex
	env      []string
	files    map[string][]byte
	tmp      string
	marker   string
	manifest string
}

func newFixture(t *testing.T, machine, downloader string) *fixture {
	t.Helper()
	f := &fixture{files: make(map[string][]byte)}
	for _, arch := range []string{"riscv64", "arm64", "amd64"} {
		name := "onekvm-installer-0.1.0-linux-" + arch + ".tar.gz"
		data := archive(t, arch)
		f.files["/"+name] = data
		f.manifest += fmt.Sprintf("%x  ./%s\n", sha256.Sum256(data), name)
	}
	// Real manifests also contain the bootstrap checksum.
	f.manifest += fmt.Sprintf("%064d  ./onekvm.sh\n", 0)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.RLock()
		defer f.mu.RUnlock()
		if r.URL.Path == "/SHA256SUMS" {
			fmt.Fprint(w, f.manifest)
		} else if data, ok := f.files[r.URL.Path]; ok {
			_, _ = w.Write(data)
		} else {
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)

	root := t.TempDir()
	bin := filepath.Join(root, "bin")
	f.tmp = filepath.Join(root, "tmp")
	f.marker = filepath.Join(root, "started")
	for _, dir := range []string{bin, f.tmp} {
		if err := os.Mkdir(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"mktemp", "awk", "sha256sum", "tar", "gzip", "chmod", "rm", downloader} {
		path, err := exec.LookPath(name)
		if err != nil {
			t.Fatalf("test dependency %s: %v", name, err)
		}
		if err := os.Symlink(path, filepath.Join(bin, name)); err != nil {
			t.Fatal(err)
		}
	}
	uname := "#!/bin/sh\ncase $1 in -s) echo Linux;; -m) echo '" + machine + "';; esac\n"
	if err := os.WriteFile(filepath.Join(bin, "uname"), []byte(uname), 0700); err != nil {
		t.Fatal(err)
	}
	// Remove inherited overrides, so fixture runs do not depend on the host.
	for _, value := range os.Environ() {
		key := strings.SplitN(value, "=", 2)[0]
		if key != "PATH" && key != "TMPDIR" && !strings.HasPrefix(key, "ONEKVM_") {
			f.env = append(f.env, value)
		}
	}
	f.env = append(f.env, "PATH="+bin, "TMPDIR="+f.tmp,
		"ONEKVM_INSTALLER_RELEASE_URL="+server.URL, "ONEKVM_TEST_MARKER="+f.marker)
	return f
}

func (f *fixture) Run(t *testing.T, shell []string, args ...string) (string, int) {
	t.Helper()
	script, err := os.ReadFile("../../onekvm.sh")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, shell[0], append(append(shell[1:], "-s", "--"), args...)...)
	cmd.Env = f.env
	cmd.Stdin = bytes.NewReader(script) // Same stdin arrangement as curl | sh.
	out, err := cmd.CombinedOutput()
	status := 0
	if err != nil {
		if exit, ok := err.(*exec.ExitError); ok {
			status = exit.ExitCode()
		} else {
			t.Fatal(err)
		}
	}
	if entries, err := os.ReadDir(f.tmp); err != nil || len(entries) != 0 {
		t.Fatalf("temporary files were not cleaned up: %v, %v\n%s", entries, err, out)
	}
	return string(out), status
}

func TestBootstrapArchitectures(t *testing.T) {
	for machine, arch := range map[string]string{
		"riscv64": "riscv64", "aarch64": "arm64", "arm64": "arm64", "x86_64": "amd64", "amd64": "amd64",
	} {
		t.Run(machine, func(t *testing.T) {
			f := newFixture(t, machine, "curl")
			out, status := f.Run(t, []string{"sh"}, "--image", "/data/an image $(literal).img")
			if status != 0 || !strings.Contains(out, "Linux "+arch) ||
				!strings.Contains(out, "arg=</data/an image $(literal).img>") {
				t.Fatalf("status %d: %s", status, out)
			}
			if _, err := os.Stat(f.marker); err != nil {
				t.Fatal("verified binary did not run:", err)
			}
		})
	}
}

func TestBootstrapRejectsInvalidRelease(t *testing.T) {
	for _, test := range []string{"unsupported architecture", "checksum mismatch", "missing package", "duplicate package", "bad checksum", "path traversal"} {
		t.Run(test, func(t *testing.T) {
			machine := "riscv64"
			if test == "unsupported architecture" {
				machine = "armv7l"
			}
			f := newFixture(t, machine, "curl")
			const name = "/onekvm-installer-0.1.0-linux-riscv64.tar.gz"
			f.mu.Lock()
			switch test {
			case "checksum mismatch":
				f.files[name] = []byte("corrupt archive")
			case "missing package":
				delete(f.files, name)
			case "duplicate package":
				f.manifest += f.manifest
			case "bad checksum":
				f.manifest = "invalid  ." + name + "\n"
			case "path traversal":
				f.manifest = fmt.Sprintf("%064d  ../%s\n", 0, name[1:])
			}
			f.mu.Unlock()
			out, status := f.Run(t, []string{"sh"})
			if status == 0 {
				t.Fatal("invalid release was accepted:", out)
			}
			if _, err := os.Stat(f.marker); !os.IsNotExist(err) {
				t.Fatal("unverified binary ran:", out)
			}
		})
	}
}

func TestBootstrapWgetAndExitStatus(t *testing.T) {
	f := newFixture(t, "riscv64", "wget")
	f.env = append(f.env, "ONEKVM_TEST_EXIT=42")
	out, status := f.Run(t, []string{"sh"}, "--version")
	if status != 42 || !strings.Contains(out, "arg=<--version>") {
		t.Fatalf("status %d: %s", status, out)
	}
}

func TestBootstrapBusyBox(t *testing.T) {
	path, err := exec.LookPath("busybox")
	if err != nil {
		t.Skip("BusyBox is not installed")
	}
	f := newFixture(t, "riscv64", "wget")
	out, status := f.Run(t, []string{path, "sh"}, "--help")
	if status != 0 || !strings.Contains(out, "arg=<--help>") {
		t.Fatalf("status %d: %s", status, out)
	}
}
