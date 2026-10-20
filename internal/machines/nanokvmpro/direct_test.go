package nanokvmpro

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/onekvm/onekvm-installer/internal/installer"
)

func testImage(t *testing.T, profile string) *os.File {
	t.Helper()
	f, err := os.Create(filepath.Join(t.TempDir(), "image.img"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { f.Close() })
	root, payload := int64(17*installer.MiB), int64(17*installer.MiB+8192)
	id, media := byte(2), byte(1)
	if profile == "pro-emmc" {
		root = proRootStart * 512
		payload = root + 8192
		id, media = 3, 2
	}
	if err = f.Truncate(payload); err != nil {
		t.Fatal(err)
	}
	if profile == "pro-sd" {
		mbr := make([]byte, 512)
		mbr[446], mbr[450], mbr[466], mbr[510], mbr[511] = 128, 14, 131, 0x55, 0xaa
		for offset, value := range map[int]uint32{440: 0x4f4b5650, 454: 2048, 458: 32768, 470: uint32(root / 512), 474: 16} {
			binary.LittleEndian.PutUint32(mbr[offset:], value)
		}
		if _, err = f.WriteAt(mbr, 0); err != nil {
			t.Fatal(err)
		}
	}
	header := make([]byte, 48)
	copy(header, "hsqs")
	binary.LittleEndian.PutUint64(header[40:], 8192)
	f.WriteAt(header, root)
	hash := sha256.New()
	if _, err = io.Copy(hash, io.NewSectionReader(f, 0, payload)); err != nil {
		t.Fatal(err)
	}
	meta := make([]byte, 480)
	copy(meta, "ONEKVM-IMG-META\x00")
	meta[20], meta[21] = id, media
	for offset, value := range map[int]uint16{16: 1, 18: 480, 32: 96, 164: uint16(len("onekvm-nanokvm-pro"))} {
		binary.LittleEndian.PutUint16(meta[offset:], value)
	}
	binary.LittleEndian.PutUint64(meta[24:], uint64(payload))
	copy(meta[36:], hash.Sum(nil))
	copy(meta[68:], "test-build")
	copy(meta[168:], "onekvm-nanokvm-pro")
	hash.Write(meta)
	if _, err = f.WriteAt(append(meta, hash.Sum(nil)...), payload); err != nil {
		t.Fatal(err)
	}
	return f
}
func TestDirectSDStreamChecksAndReadback(t *testing.T) {
	source := testImage(t, "pro-sd")
	st, _ := source.Stat()
	target, err := os.CreateTemp(t.TempDir(), "card")
	if err != nil {
		t.Fatal(err)
	}
	defer target.Close()
	target.Truncate(st.Size() + 8)
	target.WriteAt([]byte("KEEP"), st.Size()-512)
	source.Seek(0, io.SeekStart)
	hash := sha256.New()
	io.Copy(hash, source)
	digest := "sha256:" + hex.EncodeToString(hash.Sum(nil))
	var updates int
	source.Seek(0, io.SeekStart)
	info, err := streamSD(target, source, st.Size(), digest, func(done, total int64) {
		if done < 0 || done > total || total != st.Size() {
			t.Fatalf("invalid progress %d/%d", done, total)
		}
		updates++
	})
	if err != nil {
		t.Fatal(err)
	}
	if info.Profile != "pro-sd" || updates < 4 {
		t.Fatal("missing stream/readback verification")
	}
	after := make([]byte, 4)
	target.ReadAt(after, st.Size()-512)
	if string(after) != "KEEP" {
		t.Fatal("wrote metadata trailer to SD")
	}
	for _, tc := range []struct {
		name   string
		reader func() io.Reader
		size   int64
		digest string
	}{
		{"wrong digest", func() io.Reader { source.Seek(0, 0); return source }, st.Size(), "sha256:" + strings.Repeat("0", 64)},
		{"truncated", func() io.Reader { return io.NewSectionReader(source, 0, st.Size()-1) }, st.Size(), digest},
		{"extra byte", func() io.Reader {
			return io.MultiReader(io.NewSectionReader(source, 0, st.Size()), bytes.NewReader([]byte{1}))
		}, st.Size(), digest},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := streamSD(target, tc.reader(), tc.size, tc.digest, nil); err == nil {
				t.Fatal("accepted invalid stream")
			}
		})
	}
	source.WriteAt([]byte{33}, 4096)
	source.Seek(0, 0)
	if _, err = streamSD(target, source, st.Size(), "", nil); err == nil {
		t.Fatal("accepted bad payload hash")
	}
}
func TestDirectSDReadbackCorruption(t *testing.T) {
	source := testImage(t, "pro-sd")
	st, _ := source.Stat()
	f, err := os.CreateTemp(t.TempDir(), "card")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	source.Seek(0, 0)
	if _, err = streamSD(&corruptDisk{f}, source, st.Size(), "", nil); err == nil || !strings.Contains(err.Error(), "readback") {
		t.Fatal("missed readback failure", err)
	}
}

type corruptDisk struct{ *os.File }

func (f *corruptDisk) Sync() error { _, err := f.WriteAt([]byte{99}, 4096); return err }
func TestProEMMCImageGeometry(t *testing.T) {
	source := testImage(t, "pro-emmc")
	info, err := installer.Verify(source.Name(), "pro-emmc", 512*installer.MiB, nil)
	if err != nil {
		t.Fatal(err)
	}
	if info.BootStart != proBootStart || info.BootSectors != proBootSectors {
		t.Fatal(info)
	}
	if _, err = installer.Verify(source.Name(), "pro-sd", 512*installer.MiB, nil); err == nil {
		t.Fatal("accepted eMMC image for SD")
	}
	metadata, err := installer.ReadImageMetadata(source.Name())
	if err != nil || metadata.Profile != "pro-emmc" {
		t.Fatal(metadata, err)
	}
}
func TestPayloadWriterHandlesChunkBoundaries(t *testing.T) {
	for _, chunk := range []int{1, 3, 511, 512, 513, 4096} {
		var out bytes.Buffer
		writer := &payloadWriter{target: &out}
		input := bytes.Repeat([]byte{42}, 8192+512)
		for at := 0; at < len(input); at += chunk {
			if _, err := writer.Write(input[at:min(at+chunk, len(input))]); err != nil {
				t.Fatal(err)
			}
		}
		if out.Len() != 8192 || len(writer.tail) != 512 || !bytes.Equal(out.Bytes(), input[:8192]) {
			t.Fatal("wrong trailer buffering", chunk)
		}
	}
}
