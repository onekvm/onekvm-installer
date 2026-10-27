package installer

import (
	"bytes"
	"compress/gzip"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

func TestRecoveryWaitsForAsynchronousMMCEnumeration(t *testing.T) {
	root := t.TempDir()
	for _, dir := range []string{"proc", "dev", "sys/class/block/mmcblk2/device"} {
		if err := os.MkdirAll(filepath.Join(root, dir), 0755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "proc/cmdline"), []byte("root=/dev/mmcblk2p2 rootwait"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := waitRecoveryMMC(root, 10*time.Millisecond); err == nil {
		t.Fatal("recovery accepted a disk that has not appeared")
	}
	ready := make(chan error, 1)
	go func() {
		time.Sleep(10 * time.Millisecond)
		if err := os.WriteFile(filepath.Join(root, "dev/mmcblk2p2"), nil, 0600); err != nil {
			ready <- err
			return
		}
		ready <- os.WriteFile(filepath.Join(root, "sys/class/block/mmcblk2/device/type"), []byte("SD\n"), 0600)
	}()
	if err := waitRecoveryMMC(root, time.Second); err != nil {
		t.Fatal(err)
	}
	if err := <-ready; err != nil {
		t.Fatal(err)
	}
}

func TestRecoveryOverlayReplacesNativeInitAndRetainsDisplayAssets(t *testing.T) {
	base, err := compressedArchive([]archiveFile{
		{"init", 0100755, []byte("native init must not run")},
		{"usr/sbin/onekvm-boot-splash", 0100755, []byte("matching renderer")},
		{"usr/lib/onekvm-initramfs/fb_jd9853.ko", 0100644, []byte("matching driver")},
	})
	if err != nil {
		t.Fatal(err)
	}
	overlay, err := recoveryArchive([]byte("current installer PID 1"))
	if err != nil {
		t.Fatal(err)
	}
	combined, err := appendRecoveryArchive(base, overlay)
	if err != nil {
		t.Fatal(err)
	}
	// The same native-archive + padding + gzip overlay format is used for
	// the one-shot configuration hook. Check both streams and their order.
	if !bytes.Equal(combined[:len(base)], base) || !bytes.Equal(combined[len(base)+4:], overlay) {
		t.Fatal("native assets or final PID 1 archive changed")
	}
	corrupt := bytes.Clone(base)
	corrupt[len(corrupt)-5] ^= 1
	if _, err := appendRecoveryArchive(corrupt, overlay); err == nil {
		t.Fatal("accepted a corrupt native initramfs")
	}
}

func TestRewriteBootPayloadReusesTheExistingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "boot.sd")
	if err := os.WriteFile(path, bytes.Repeat([]byte{1}, 8192), 0600); err != nil {
		t.Fatal(err)
	}
	before, _ := os.Stat(path)
	for _, size := range []int{8500, 100} {
		payload := bytes.Repeat([]byte{2}, size)
		if err := RewriteBootPayload(path, payload); err != nil {
			t.Fatal(err)
		}
		after, _ := os.Stat(path)
		if !os.SameFile(before, after) {
			t.Fatal("allocated a second complete boot payload")
		}
		actual, err := os.ReadFile(path)
		if err != nil || !bytes.Equal(actual, payload) {
			t.Fatal("boot payload growth/truncation failed", err)
		}
		if _, err := os.Stat(path + ".tmp"); !os.IsNotExist(err) {
			t.Fatal("boot rewrite created a complete temporary copy")
		}
	}
}

func TestRecoveryArchiveIsExecutablePID1(t *testing.T) {
	payload := []byte("exact static recovery executable")
	archive, err := recoveryArchive(payload)
	if err != nil {
		t.Fatal(err)
	}
	z, err := gzip.NewReader(bytes.NewReader(archive))
	if err != nil {
		t.Fatal(err)
	}
	raw, err := io.ReadAll(z)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for pos := 0; pos < len(raw); {
		if len(raw)-pos < 110 || string(raw[pos:pos+6]) != "070701" {
			t.Fatal("invalid newc archive")
		}
		field := func(off int) int {
			n, err := strconv.ParseUint(string(raw[pos+off:pos+off+8]), 16, 32)
			if err != nil {
				t.Fatal(err)
			}
			return int(n)
		}
		mode, size, nameSize := field(14), field(54), field(94)
		name := string(raw[pos+110 : pos+110+nameSize-1])
		names = append(names, name)
		start := (pos + 110 + nameSize + 3) &^ 3
		if name == "init" && (mode != 0100755 || !bytes.Equal(raw[start:start+size], payload)) {
			t.Fatal("wrong PID 1 mode or contents")
		}
		pos = (start + size + 3) &^ 3
	}
	if len(names) != 2 || names[0] != "init" || names[1] != "TRAILER!!!" {
		t.Fatal(names)
	}
}
