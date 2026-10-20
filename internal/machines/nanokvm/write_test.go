package nanokvm

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"testing"

	"github.com/onekvm/onekvm-installer/internal/installer"
)

func TestWritePayloadBoundsAndReadback(t *testing.T) {
	payload := bytes.Repeat([]byte{42}, 4096)
	hash := sha256.Sum256(payload)
	info := installer.Image{Profile: "nanokvm", PayloadSize: 4096, PayloadSHA256: hex.EncodeToString(hash[:])}
	f, err := os.CreateTemp(t.TempDir(), "disk")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	_ = f.Truncate(8192)
	_, _ = f.WriteAt([]byte("KEEP"), 4096)
	if err = (Machine{}).WritePayload(f, bytes.NewReader(append(payload, []byte("TRAILER")...)), info, nil); err != nil {
		t.Fatal(err)
	}
	b := make([]byte, 4)
	_, _ = f.ReadAt(b, 4096)
	if string(b) != "KEEP" {
		t.Fatal("copied trailer / overran payload")
	}
	info.Profile = "pro-emmc"
	_, _ = f.Seek(0, io.SeekStart)
	if err = (Machine{}).WritePayload(f, bytes.NewReader(payload), info, nil); err == nil {
		t.Fatal("accepted full-disk eMMC write")
	}
	info.Profile = "nanokvm"
	_, _ = f.Seek(0, io.SeekStart)
	if err = (Machine{}).WritePayload(f, bytes.NewReader(payload[:2048]), info, nil); err == nil {
		t.Fatal("accepted short source")
	}
	_, _ = f.Seek(0, io.SeekStart)
	if err = (Machine{}).WritePayload(&badDisk{f}, bytes.NewReader(payload), info, nil); err == nil {
		t.Fatal("missed readback corruption")
	}
}

type badDisk struct{ *os.File }

func (b *badDisk) Sync() error { _, err := b.WriteAt([]byte{99}, 0); return err }
