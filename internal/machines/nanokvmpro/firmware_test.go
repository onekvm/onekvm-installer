package nanokvmpro

import (
	"bytes"
	"context"
	"io"
	"os"
	"testing"

	"github.com/onekvm/onekvm-installer/internal/installer"
)

type memorySPL struct {
	data           []byte
	writes         int
	short, corrupt bool
}

func (m *memorySPL) ReadAt(out []byte, off int64) (int, error) {
	n := copy(out, m.data[off:])
	if n < len(out) {
		return n, io.EOF
	}
	return n, nil
}
func (m *memorySPL) WriteAt(in []byte, off int64) (int, error) {
	m.writes++
	if off != 0 || len(in) != proSPLSize {
		panic("SPL write escaped the primary region")
	}
	if m.short {
		return copy(m.data, in[:len(in)/2]), nil
	}
	n := copy(m.data, in)
	if m.corrupt {
		m.data[0] ^= 1
	}
	return n, nil
}
func (m *memorySPL) Sync() error { return nil }

// Opt-in fixture from the verified official image. No device I/O is used.
func TestBundledSPLDeltaAndBoundedWriter(t *testing.T) {
	path := os.Getenv("ONEKVM_TEST_STOCK_SPL")
	if path == "" {
		t.Skip("set ONEKVM_TEST_STOCK_SPL to the verified factory primary SPL")
	}
	source, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	target, err := reconstructSPL(source)
	if err != nil {
		t.Fatal(err)
	}
	if digest(target) != proSPLTargetSHA || len(target) != proSPLSize {
		t.Fatal("delta did not reconstruct the pinned complete package")
	}
	for _, state := range []string{"valid", "source-changed", "short-write", "readback-failed", "bad-target"} {
		t.Run(state, func(t *testing.T) {
			data := append(bytes.Clone(source), bytes.Repeat([]byte{0xa5}, proSPLSize)...)
			device := &memorySPL{data: data}
			candidate := bytes.Clone(target)
			switch state {
			case "source-changed":
				device.data[0] ^= 1
			case "short-write":
				device.short = true
			case "readback-failed":
				device.corrupt = true
			case "bad-target":
				candidate[0] ^= 1
			}
			err := writePrimarySPL(device, source, candidate)
			if (err == nil) != (state == "valid") {
				t.Fatal("wrong firmware write result", err)
			}
			if (state == "source-changed" || state == "bad-target") && device.writes != 0 {
				t.Fatal("failed validation wrote firmware")
			}
			if !bytes.Equal(device.data[proSPLSize:], bytes.Repeat([]byte{0xa5}, proSPLSize)) {
				t.Fatal("write changed protected bytes")
			}
		})
	}
}

func TestSPLDeltaRejectsMalformedStreamsAndUnknownSource(t *testing.T) {
	for _, patch := range [][]byte{nil, []byte("BSDIFF40"), append([]byte("BSDIFF40"), make([]byte, 24)...), bytes.Repeat([]byte{0xff}, 64)} {
		if _, err := applySPLDelta(make([]byte, proSPLSize), patch); err == nil {
			t.Fatal("malformed delta accepted")
		}
	}
	if _, err := reconstructSPL(make([]byte, proSPLSize)); err == nil {
		t.Fatal("unknown factory firmware accepted")
	}
}

func TestSDFirmwareRepairValidatesTheSelectedImage(t *testing.T) {
	path := os.Getenv("ONEKVM_TEST_SD_IMAGE")
	if path == "" {
		t.Skip("set ONEKVM_TEST_SD_IMAGE to a complete matching Pro SD image")
	}
	info, err := installer.Verify(path, "pro-sd", 4*1024*installer.MiB, nil)
	if err != nil {
		t.Fatal(err)
	}
	in := installer.CheckContext{Root: t.TempDir(), Image: info, ImagePath: path}
	// This root has no old SD contents: firmware comes from the selected image.
	if err := checkSDFirmware(context.Background(), in); err != nil {
		t.Fatal("matching selected image was rejected", err)
	}
	in.Image.BuildID += "-changed"
	if err := checkSDFirmware(context.Background(), in); err == nil {
		t.Fatal("changed image identity was accepted")
	}
}
