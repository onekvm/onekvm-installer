package installer

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

var trailerMagic = []byte("ONEKVM-IMG-META\x00")

type Image struct {
	Profile       string `json:"profile"`
	Media         string `json:"media"`
	Machine       string `json:"machine"`
	BuildID       string `json:"build_id"`
	PayloadSize   int64  `json:"payload_size"`
	PayloadSHA256 string `json:"payload_sha256"`
	ImageSHA256   string `json:"image_sha256"`
	BootStart     int64  `json:"boot_start"`
	BootSectors   int64  `json:"boot_sectors"`
}

type imageStream struct {
	io.Reader
	close func() error
}

func (s *imageStream) Close() error { return s.close() }

// Release images are raw .img files. Their bytes, including the metadata
// trailer, are retained unchanged through staging and the RAM copy.
func OpenImage(path string) (io.ReadCloser, error) {
	return openImage(path, nil)
}

// Progress measures actual file bytes without trusting an unverified trailer.
func openImage(path string, progress Progress) (io.ReadCloser, error) {
	if !strings.EqualFold(filepath.Ext(path), ".img") {
		return nil, fmt.Errorf("only raw .img images are supported")
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	s, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, err
	}
	if !s.Mode().IsRegular() {
		f.Close()
		return nil, fmt.Errorf("image must be a regular .img file")
	}
	if progress != nil {
		progress(0, s.Size())
	}
	r := &progressReader{Reader: f, total: s.Size(), progress: progress}
	return &imageStream{r, f.Close}, nil
}

type progressReader struct {
	io.Reader
	done, total int64
	progress    Progress
}

func (r *progressReader) Read(b []byte) (int, error) {
	n, err := r.Reader.Read(b)
	r.done += int64(n)
	if r.progress != nil && n > 0 {
		r.progress(r.done, r.total)
	}
	return n, err
}

func fixedASCII(raw []byte) (string, error) {
	n := bytes.IndexByte(raw, 0)
	if n < 1 || !allZero(raw[n:]) {
		return "", fmt.Errorf("invalid metadata string padding")
	}
	for _, c := range raw[:n] {
		if c < 32 || c > 126 {
			return "", fmt.Errorf("invalid metadata string")
		}
	}
	return string(raw[:n]), nil
}
func allZero(b []byte) bool {
	for _, c := range b {
		if c != 0 {
			return false
		}
	}
	return true
}

func parseMetadata(t []byte, size int64) (Image, error) {
	var info Image
	if len(t) != 512 || !bytes.Equal(t[:16], trailerMagic) {
		return info, fmt.Errorf("missing OneKVM metadata; legacy/vendor images are rejected")
	}
	u16 := func(i int) uint16 { return binary.LittleEndian.Uint16(t[i:]) }
	if u16(16) != 1 || u16(18) != 480 || u16(22) != 0 || u16(32) != 96 || u16(34) != 0 || u16(166) != 0 || !allZero(t[232:480]) {
		return info, fmt.Errorf("unsupported metadata version, lengths or flags")
	}
	profiles := map[byte]string{1: "nanokvm", 2: "pro-sd", 3: "pro-emmc"}
	info.Profile = profiles[t[20]]
	media := byte(1)
	info.Media = "SD"
	info.Machine = "onekvm-nanokvm"
	if info.Profile == "pro-emmc" {
		media = 2
		info.Media = "MMC"
	}
	if info.Profile == "pro-emmc" || info.Profile == "pro-sd" {
		info.Machine = "onekvm-nanokvm-pro"
	}
	if info.Profile == "" || t[21] != media {
		return info, fmt.Errorf("invalid image profile/media")
	}
	machine, err := fixedASCII(t[168:232])
	if err != nil {
		return info, err
	}
	if machine != info.Machine || int(u16(164)) != len(machine) {
		return info, fmt.Errorf("invalid image machine")
	}
	info.BuildID, err = fixedASCII(t[68:164])
	if err != nil {
		return info, err
	}
	payload := binary.LittleEndian.Uint64(t[24:32])
	if payload == 0 || payload%512 != 0 || payload > uint64(size) || payload+512 != uint64(size) {
		return info, fmt.Errorf("truncated image or invalid payload length")
	}
	info.PayloadSize = int64(payload)
	info.PayloadSHA256 = hex.EncodeToString(t[36:68])
	info.ImageSHA256 = hex.EncodeToString(t[480:])
	return info, nil
}

// Verify retains only a chunk and the trailer; it checks the exact bytes that
// will be written and the legacy final hash (all bytes except its own 32 bytes).
func Verify(path, expected string, maxBytes int64, progress Progress) (Image, error) {
	return VerifyContext(context.Background(), path, expected, maxBytes, progress)
}

// VerifyContext keeps hashing interruptible while a wizard download is checked.
func VerifyContext(ctx context.Context, path, expected string, maxBytes int64, progress Progress) (Image, error) {
	var zero Image
	r, err := openImage(path, progress)
	if err != nil {
		return zero, err
	}
	defer r.Close()
	return VerifyStream(contextReader{ctx, r}, expected, maxBytes)
}

type contextReader struct {
	ctx context.Context
	io.Reader
}

func (r contextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.Reader.Read(p)
}

// ReadImageMetadata selects a target before installation; Verify still checks
// every byte before a local image can be written.
func ReadImageMetadata(path string) (Image, error) {
	if !strings.EqualFold(filepath.Ext(path), ".img") {
		return Image{}, fmt.Errorf("only raw .img images are supported")
	}
	f, err := os.Open(path)
	if err != nil {
		return Image{}, err
	}
	defer f.Close()
	s, err := f.Stat()
	if err != nil {
		return Image{}, err
	}
	if !s.Mode().IsRegular() || s.Size() < 512 {
		return Image{}, fmt.Errorf("invalid image file")
	}
	tail := make([]byte, 512)
	if _, err = f.ReadAt(tail, s.Size()-512); err != nil {
		return Image{}, err
	}
	return parseMetadata(tail, s.Size())
}

func VerifyStream(r io.Reader, expected string, maxBytes int64) (Image, error) {
	var zero Image
	if maxBytes <= 0 {
		maxBytes = 4 * 1024 * MiB
	}
	h := sha256.New()
	tail := make([]byte, 0, 512)
	buf := make([]byte, MiB)
	var total int64
	var header []byte
	// Capture the SquashFS header during the same checksum scan.
	var rootHeader []byte
	var rootOffset int64
	const proRootOffset = int64(291241984)
	proRootHeader := make([]byte, 48)
	var proRootBytes int
	prefixZero := true
	for {
		n, readErr := io.ReadFull(r, buf)
		if n > 0 {
			if total == 0 && n >= 512 {
				header = bytes.Clone(buf[:512])
				rootOffset = int64(binary.LittleEndian.Uint32(header[470:474])) * 512
			}
			if rootOffset > 0 && total < rootOffset+48 && total+int64(n) > rootOffset {
				if rootHeader == nil {
					rootHeader = make([]byte, 48)
				}
				start, end := max(total, rootOffset), min(total+int64(n), rootOffset+48)
				copy(rootHeader[start-rootOffset:end-rootOffset], buf[start-total:end-total])
			}
			if total < proRootOffset+48 && total+int64(n) > proRootOffset {
				start, end := max(total, proRootOffset), min(total+int64(n), proRootOffset+48)
				proRootBytes += copy(proRootHeader[start-proRootOffset:end-proRootOffset], buf[start-total:end-total])
			}
			if total < 22806528 {
				prefixZero = prefixZero && allZero(buf[:min(int64(n), 22806528-total)])
			}
			total += int64(n)
			if total > maxBytes {
				return zero, fmt.Errorf("image exceeds target capacity")
			}
			combined := append(tail, buf[:n]...)
			if len(combined) > 512 {
				h.Write(combined[:len(combined)-512])
			}
			tail = bytes.Clone(combined[max(0, len(combined)-512):])
		}
		if readErr == io.EOF || readErr == io.ErrUnexpectedEOF {
			break
		}
		if readErr != nil {
			return zero, readErr
		}
	}
	info, err := parseMetadata(tail, total)
	if err != nil {
		return zero, err
	}
	if expected != "" && info.Profile != expected {
		return zero, fmt.Errorf("wrong image type: expected %s, got %s", expected, info.Profile)
	}
	if hex.EncodeToString(h.Sum(nil)) != info.PayloadSHA256 {
		return zero, fmt.Errorf("payload SHA-256 mismatch")
	}
	h.Write(tail[:480])
	if hex.EncodeToString(h.Sum(nil)) != info.ImageSHA256 {
		return zero, fmt.Errorf("trailer SHA-256 mismatch")
	}
	if info.Profile == "pro-emmc" {
		if !prefixZero || info.PayloadSize <= proRootOffset || info.PayloadSize > proRootOffset+128*MiB || proRootBytes != 48 || string(proRootHeader[:4]) != "hsqs" {
			return zero, fmt.Errorf("unsupported Pro eMMC payload geometry")
		}
		used := binary.LittleEndian.Uint64(proRootHeader[40:])
		if used < 4096 || used > uint64(info.PayloadSize-proRootOffset) {
			return zero, fmt.Errorf("invalid SquashFS payload")
		}
		info.BootStart, info.BootSectors = 306688, 262144
	} else {
		if len(header) != 512 {
			return zero, fmt.Errorf("missing MBR")
		}
		info.BootStart, info.BootSectors, err = sdGeometry(header, info.PayloadSize, info.Profile)
		if err != nil {
			return zero, err
		}
		u32 := func(i int) uint32 { return binary.LittleEndian.Uint32(header[i:]) }
		if len(rootHeader) != 48 || string(rootHeader[:4]) != "hsqs" || binary.LittleEndian.Uint64(rootHeader[40:]) < 4096 ||
			binary.LittleEndian.Uint64(rootHeader[40:]) > uint64(u32(474))*512 {
			return zero, fmt.Errorf("invalid SquashFS payload")
		}
	}
	return info, nil
}

// A release carrier contains a boot filesystem sized for its files and a
// SquashFS partition sized for its payload. Firstboot builds the final layout.
func sdGeometry(header []byte, payload int64, profile string) (int64, int64, error) {
	if len(header) != 512 {
		return 0, 0, fmt.Errorf("missing MBR")
	}
	boot, diskID := int64(1), uint32(0x4f4b564d)
	if profile == "pro-sd" {
		boot, diskID = 2048, 0x4f4b5650
	}
	u32 := func(i int) uint32 { return binary.LittleEndian.Uint32(header[i:]) }
	size := int64(u32(458))
	root := (boot + size + 2047) / 2048 * 2048
	if !bytes.Equal(header[510:], []byte{0x55, 0xaa}) || u32(440) != diskID || header[446] != 128 || (header[450] != 12 && header[450] != 14) ||
		int64(u32(454)) != boot || size < 32768 || size > 262144 || size%2048 != 0 || header[466] != 131 || int64(u32(470)) != root || u32(474) == 0 || u32(474) > 262144 ||
		(root+int64(u32(474)))*512 != payload || !allZero(header[478:510]) {
		return 0, 0, fmt.Errorf("unsupported SD carrier geometry")
	}
	return boot, size, nil
}

// WithProgress reports bytes read from r, using the known input length.
func WithProgress(r io.Reader, total int64, progress Progress) io.Reader {
	return &progressReader{Reader: r, total: total, progress: progress}
}
