package bootstrap_test

import (
	"bytes"
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

type fixture struct {
	mu       sync.RWMutex
	env      []string
	files    map[string][]byte
	tmp      string
	marker   string
	manifest string
}

func newFixture(t *testing.T, machine string) *fixture {
	t.Helper()
	f := &fixture{files: make(map[string][]byte)}
	for _, arch := range []string{"riscv64", "arm64"} {
		name := "onekvm-installer-0.1.0-linux-" + arch
		data := []byte(fixtureBinary)
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
	for _, name := range []string{"mktemp", "awk", "sha256sum", "chmod", "rm", "curl"} {
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
		"riscv64": "riscv64", "aarch64": "arm64", "arm64": "arm64",
	} {
		t.Run(machine, func(t *testing.T) {
			f := newFixture(t, machine)
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
			f := newFixture(t, machine)
			const name = "/onekvm-installer-0.1.0-linux-riscv64"
			f.mu.Lock()
			switch test {
			case "checksum mismatch":
				f.files[name] = []byte("corrupt binary")
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

func TestBootstrapExitStatus(t *testing.T) {
	f := newFixture(t, "riscv64")
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
	script, err := os.Open("../../onekvm.sh")
	if err != nil {
		t.Fatal(err)
	}
	defer script.Close()
	cmd := exec.Command(path, "sh", "-n")
	cmd.Stdin = script
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("BusyBox sh -n failed: %v\n%s", err, output)
	}
}
