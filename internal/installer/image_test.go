package installer

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func fixtureImage(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "valid.img")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	payload := int64((75776 + 16) * 512)
	if err = f.Truncate(payload); err != nil {
		t.Fatal(err)
	}
	mbr := make([]byte, 512)
	mbr[446] = 128
	mbr[450] = 12
	mbr[466] = 131
	for offset, value := range map[int]uint32{440: 0x4f4b564d, 454: 1, 458: 73728, 470: 75776, 474: 16} {
		binary.LittleEndian.PutUint32(mbr[offset:], value)
	}
	mbr[510] = 0x55
	mbr[511] = 0xaa
	if _, err = f.WriteAt(mbr, 0); err != nil {
		t.Fatal(err)
	}
	squash := make([]byte, 48)
	copy(squash, "hsqs")
	binary.LittleEndian.PutUint64(squash[40:], 8192)
	if _, err = f.WriteAt(squash, 75776*512); err != nil {
		t.Fatal(err)
	}
	h := sha256.New()
	if _, err = io.Copy(h, io.NewSectionReader(f, 0, payload)); err != nil {
		t.Fatal(err)
	}
	meta := make([]byte, 480)
	copy(meta, trailerMagic)
	for offset, value := range map[int]uint16{16: 1, 18: 480, 32: 96, 164: 14} {
		binary.LittleEndian.PutUint16(meta[offset:], value)
	}
	meta[20] = 1
	meta[21] = 1
	binary.LittleEndian.PutUint64(meta[24:], uint64(payload))
	copy(meta[36:], h.Sum(nil))
	copy(meta[68:], "test-build")
	copy(meta[168:], "onekvm-nanokvm")
	// The machine string is 14 bytes (checked below to avoid fixture drift).
	binary.LittleEndian.PutUint16(meta[164:], uint16(len("onekvm-nanokvm")))
	h.Write(meta)
	if _, err = f.WriteAt(append(meta, h.Sum(nil)...), payload); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestVerifyRaw(t *testing.T) {
	path := fixtureImage(t)
	s, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	last := int64(-1)
	info, err := Verify(path, "nanokvm", 512*MiB, func(done, total int64) {
		if total != s.Size() || done < last || done < 0 || done > total {
			t.Errorf("invalid input progress: %d/%d, previous=%d", done, total, last)
		}
		last = done
	})
	if err != nil {
		t.Fatal(err)
	}
	if info.BuildID != "test-build" || info.PayloadSize != (75776+16)*512 {
		t.Fatal(info)
	}
	if last != s.Size() {
		t.Fatalf("progress did not reach file size: %d/%d", last, s.Size())
	}
	if _, err = Verify(path, "pro-emmc", 512*MiB, nil); err == nil {
		t.Fatal("accepted wrong media")
	}
}

func TestRejectCorruptAndLegacy(t *testing.T) {
	path := fixtureImage(t)
	f, _ := os.OpenFile(path, os.O_RDWR, 0)
	_, _ = f.WriteAt([]byte{1}, 1024)
	f.Close()
	if _, err := Verify(path, "nanokvm", 512*MiB, nil); err == nil || !strings.Contains(err.Error(), "SHA-256") {
		t.Fatalf("corrupt image: %v", err)
	}
	legacy := filepath.Join(t.TempDir(), "legacy.img")
	_ = os.WriteFile(legacy, make([]byte, 4096), 0600)
	if _, err := Verify(legacy, "", MiB, nil); err == nil {
		t.Fatal("accepted legacy image")
	}
	if _, err := Verify(path, "", MiB, nil); err == nil {
		t.Fatal("accepted image beyond limit")
	}
}

func TestMetadataBounds(t *testing.T) {
	path := fixtureImage(t)
	f, _ := os.Open(path)
	defer f.Close()
	stat, _ := f.Stat()
	tail := make([]byte, 512)
	_, _ = f.ReadAt(tail, stat.Size()-512)
	for _, offset := range []int{16, 18, 20, 21, 22, 24, 32, 34, 164, 166, 232} {
		bad := bytes.Clone(tail)
		bad[offset] ^= 255
		if _, err := parseMetadata(bad, stat.Size()); err == nil {
			t.Errorf("accepted corrupt metadata at %d", offset)
		}
	}
}

func TestRejectCompressedInputs(t *testing.T) {
	for _, name := range []string{"image.img.gz", "image.img.xz"} {
		path := filepath.Join(t.TempDir(), name)
		if err := os.WriteFile(path, []byte("not raw"), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := Verify(path, "nanokvm", MiB, nil); err == nil || !strings.Contains(err.Error(), "only raw .img") {
			t.Fatalf("accepted compressed image %s: %v", name, err)
		}
	}
	for _, magic := range [][]byte{{0x1f, 0x8b, 8}, {0xfd, '7', 'z', 'X', 'Z', 0}} {
		path := filepath.Join(t.TempDir(), "renamed.img")
		data := append(magic, make([]byte, 1024)...)
		if err := os.WriteFile(path, data, 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := Verify(path, "nanokvm", MiB, nil); err == nil {
			t.Fatal("accepted renamed compressed input")
		}
	}
}

func TestSDCarrierGeometry(t *testing.T) {
	for _, profile := range []string{"nanokvm", "pro-sd"} {
		for _, mib := range []int64{16, 32, 36, 128} {
			t.Run(fmt.Sprintf("%s/%d", profile, mib), func(t *testing.T) {
				boot, diskID := uint32(1), uint32(0x4f4b564d)
				if profile == "pro-sd" {
					boot, diskID = 2048, 0x4f4b5650
				}
				sectors := uint32(mib * 2048)
				root := (boot + sectors + 2047) / 2048 * 2048
				mbr := make([]byte, 512)
				mbr[446], mbr[450], mbr[466] = 128, 14, 131
				mbr[510], mbr[511] = 0x55, 0xaa
				for off, val := range map[int]uint32{440: diskID, 454: boot, 458: sectors, 470: root, 474: 16} {
					binary.LittleEndian.PutUint32(mbr[off:], val)
				}
				payload := (int64(root) + 16) * 512
				start, count, err := sdGeometry(mbr, payload, profile)
				if err != nil || start != int64(boot) || count != int64(sectors) {
					t.Fatal(start, count, err)
				}
				for _, off := range []int{440, 454, 458, 470, 474, 478, 510} {
					bad := bytes.Clone(mbr)
					bad[off] ^= 1
					if _, _, err := sdGeometry(bad, payload, profile); err == nil {
						t.Errorf("accepted invalid carrier at %d", off)
					}
				}
				if _, _, err := sdGeometry(mbr, payload+512, profile); err == nil {
					t.Fatal("accepted extra payload")
				}
			})
		}
	}
}
