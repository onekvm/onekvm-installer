package nanokvmpro

import (
	"bytes"
	"compress/gzip"
	"encoding/binary"
	"hash/crc32"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/onekvm/onekvm-installer/internal/installer"
)

func proTestRawKernel() []byte {
	p := make([]byte, 4096)
	copy(p[56:], "ARM\x64")
	binary.LittleEndian.PutUint64(p[16:], uint64(len(p)))
	return p
}

func proTestCompressedKernel(t *testing.T, raw []byte) []byte {
	t.Helper()
	var data bytes.Buffer
	z := gzip.NewWriter(&data)
	if _, err := z.Write(raw); err != nil {
		t.Fatal(err)
	}
	if err := z.Close(); err != nil {
		t.Fatal(err)
	}
	p := make([]byte, 64)
	binary.BigEndian.PutUint32(p, 0x27051956)
	binary.BigEndian.PutUint32(p[12:], uint32(data.Len()))
	binary.BigEndian.PutUint32(p[16:], 0x41000000)
	binary.BigEndian.PutUint32(p[20:], 0x41000000)
	binary.BigEndian.PutUint32(p[24:], crc32.ChecksumIEEE(data.Bytes()))
	p[28], p[29], p[30], p[31] = 5, 22, 2, 1
	binary.BigEndian.PutUint32(p[4:], crc32.ChecksumIEEE(p))
	return append(p, data.Bytes()...)
}

func TestProKernelBootstrapFormats(t *testing.T) {
	for _, compressed := range []bool{false, true} {
		p, address, command := proTestRawKernel(), "0x41000000", "booti"
		if compressed {
			p, address, command = proTestCompressedKernel(t, p), "0x45000000", "bootm"
		}
		dir := t.TempDir()
		for _, name := range []string{"board.dtb", "initramfs.uimg"} {
			put(t, dir, name, "payload")
		}
		if err := os.WriteFile(filepath.Join(dir, "Image"), p, 0600); err != nil {
			t.Fatal(err)
		}
		if err := validateBootstrapPayload("Image", p); err != nil {
			t.Fatal(err)
		}
		req := installer.Request{Device: installer.Device{Profile: "pro-emmc"}, Token: strings.Repeat("a", 64)}
		if err := bindProBootstrap(dir, req); err != nil {
			t.Fatal(err)
		}
		script, err := os.ReadFile(filepath.Join(dir, "install.scr"))
		if err != nil {
			t.Fatal(err)
		}
		for _, want := range []string{"mmc read " + address, command + " " + address + " 0x49000000 0x47000000", "initrd_high 0xffffffffffffffff"} {
			if !bytes.Contains(script, []byte(want)) {
				t.Fatalf("bootstrap missing %q", want)
			}
		}
	}
}

func TestProKernelRejectsCorruptionAndUnsafeLayout(t *testing.T) {
	valid := proTestCompressedKernel(t, proTestRawKernel())
	// Recompute CRCs so semantic header checks are tested independently.
	for _, offset := range []int{0, 12, 16, 20, 28, 29, 30, 31, 64, len(valid) - 1} {
		p := bytes.Clone(valid)
		p[offset] ^= 1
		binary.BigEndian.PutUint32(p[24:], crc32.ChecksumIEEE(p[64:]))
		clear(p[4:8])
		binary.BigEndian.PutUint32(p[4:], crc32.ChecksumIEEE(p[:64]))
		if _, _, err := proKernelBoot(p); err == nil {
			t.Fatalf("accepted corrupt kernel at %d", offset)
		}
	}
	for _, offset := range []int{4, 24} {
		p := bytes.Clone(valid)
		p[offset] ^= 1
		if _, _, err := proKernelBoot(p); err == nil {
			t.Fatal("accepted bad CRC")
		}
	}
	for _, offset := range []int{8, 16, 56} {
		raw := proTestRawKernel()
		if offset == 16 {
			binary.LittleEndian.PutUint64(raw[16:], 32*installer.MiB+1)
		} else {
			raw[offset] ^= 1
		}
		if _, _, err := proKernelBoot(proTestCompressedKernel(t, raw)); err == nil {
			t.Fatal("accepted unsafe decompressed kernel")
		}
	}
	raw := make([]byte, 32*installer.MiB+1)
	copy(raw, proTestRawKernel())
	if _, _, err := proKernelBoot(proTestCompressedKernel(t, raw)); err == nil {
		t.Fatal("accepted oversized decompressed kernel")
	}
	if _, _, err := proKernelBoot(valid[:len(valid)-1]); err == nil {
		t.Fatal("accepted truncated kernel")
	}
}
