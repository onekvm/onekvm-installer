package installer

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"strings"
	"unicode/utf16"
)

// Read only the known FAT16 carrier made by the OE image builder. No loop
// mounts, host mtools, or mounting of an untrusted image are required.
func FatFile(r io.ReaderAt, name string, limit int64) ([]byte, error) {
	return FatFileProgress(r, name, limit, nil)
}

// FatFreeBytes counts free data clusters in a builder FAT16 carrier. Configuration
// preservation checks this before erasing the target; no image mount is needed.
func FatFreeBytes(r io.ReaderAt) (int64, error) {
	header := make([]byte, 512)
	if _, err := r.ReadAt(header, 0); err != nil {
		return 0, err
	}
	u16 := func(i int) int64 { return int64(binary.LittleEndian.Uint16(header[i:])) }
	cluster, reserved, fats, fatBytes := int64(header[13])*512, u16(14)*512, int64(header[16]), u16(22)*512
	total := u16(19)
	if total == 0 {
		total = int64(binary.LittleEndian.Uint32(header[32:]))
	}
	rootBytes := (u16(17)*32 + 511) / 512 * 512
	if u16(11) != 512 || cluster < 512 || cluster > 65536 || cluster&(cluster-1) != 0 || reserved < 512 ||
		fats < 1 || fats > 2 || fatBytes < 512 || fatBytes > MiB || rootBytes < 32 || rootBytes > MiB ||
		total < 32768 || total > 262144 || !bytes.Equal(header[510:], []byte{0x55, 0xaa}) {
		return 0, fmt.Errorf("unsupported configuration boot filesystem")
	}
	clusters := (total*512 - reserved - fats*fatBytes - rootBytes) / cluster
	if clusters < 1 || clusters >= 0xffee || (clusters+2)*2 > fatBytes {
		return 0, fmt.Errorf("invalid configuration FAT geometry")
	}
	fat := make([]byte, fatBytes)
	if _, err := r.ReadAt(fat, reserved); err != nil {
		return 0, err
	}
	var free int64
	for i := int64(2); i < clusters+2; i++ {
		if binary.LittleEndian.Uint16(fat[i*2:]) == 0 {
			free += cluster
		}
	}
	return free, nil
}

func FatFileProgress(r io.ReaderAt, name string, limit int64, progress Progress) ([]byte, error) {
	b := make([]byte, 512)
	if _, err := r.ReadAt(b, 0); err != nil {
		return nil, err
	}
	u16 := func(i int) int64 { return int64(binary.LittleEndian.Uint16(b[i:])) }
	sector := u16(11)
	cluster := int64(b[13]) * sector
	reserved := u16(14) * sector
	fats := int64(b[16])
	fatSize := u16(22) * sector
	rootSize := ((u16(17)*32 + sector - 1) / max(1, sector)) * sector
	total := u16(19)
	if total == 0 {
		total = int64(binary.LittleEndian.Uint32(b[32:]))
	}
	rootOffset := reserved + fats*fatSize
	dataOffset := rootOffset + rootSize
	if sector != 512 || cluster < 512 || cluster > 65536 || cluster&(cluster-1) != 0 || fats < 1 || fats > 2 || fatSize < 512 || fatSize > MiB ||
		rootSize < 32 || rootSize > MiB || total < 32768 || total > 262144 || total%2048 != 0 || u16(22) == 0 || dataOffset >= total*512 || !bytes.Equal(b[510:], []byte{0x55, 0xaa}) {
		return nil, fmt.Errorf("unsupported boot filesystem: expected builder FAT16")
	}
	fat := make([]byte, fatSize)
	if _, err := r.ReadAt(fat, reserved); err != nil {
		return nil, err
	}
	root := make([]byte, rootSize)
	if _, err := r.ReadAt(root, rootOffset); err != nil {
		return nil, err
	}
	var longName []uint16
	var longChecksum byte
	nextOrdinal := 0
	for i := 0; i+32 <= len(root); i += 32 {
		e := root[i : i+32]
		if e[0] == 0 {
			break
		}
		if e[0] == 0xe5 {
			longName = nil
			continue
		}
		if e[11] == 0xf {
			index := int(e[0] & 0x1f)
			if index < 1 || index > 20 {
				longName = nil
				continue
			}
			if e[0]&0x40 != 0 {
				longName = make([]uint16, index*13)
				longChecksum = e[13]
				nextOrdinal = index
			}
			if len(longName) < index*13 || longChecksum != e[13] || index != nextOrdinal || e[12] != 0 || e[26] != 0 || e[27] != 0 || e[0]&0xa0 != 0 {
				longName = nil
				continue
			}
			nextOrdinal--
			part := append(append(bytes.Clone(e[1:11]), e[14:26]...), e[28:32]...)
			for j := 0; j < 13; j++ {
				longName[(index-1)*13+j] = binary.LittleEndian.Uint16(part[j*2:])
			}
			continue
		}
		long := longName
		longName = nil
		if e[11]&0x18 != 0 {
			continue
		}
		short := strings.TrimSpace(string(e[:8]))
		ext := strings.TrimSpace(string(e[8:11]))
		if ext != "" {
			short += "." + ext
		}
		var checksum byte
		for _, c := range e[:11] {
			checksum = (checksum >> 1) | (checksum << 7)
			checksum += c
		}
		end := 0
		for end < len(long) && long[end] != 0 && long[end] != 0xffff {
			end++
		}
		if string(e[:11]) != name && !strings.EqualFold(short, name) && !(nextOrdinal == 0 && checksum == longChecksum && strings.EqualFold(string(utf16.Decode(long[:end])), name)) {
			continue
		}
		size := int64(binary.LittleEndian.Uint32(e[28:]))
		start := int64(binary.LittleEndian.Uint16(e[26:]))
		if size <= 0 || size > limit {
			return nil, fmt.Errorf("invalid size for %s", name)
		}
		out := make([]byte, size)
		pos := int64(0)
		if progress != nil {
			progress(0, size)
		}
		seen := map[int64]bool{}
		for pos < size {
			if start < 2 || start >= 0xfff0 || start*2+2 > int64(len(fat)) || seen[start] {
				return nil, fmt.Errorf("invalid FAT16 cluster chain")
			}
			seen[start] = true
			offset := dataOffset + (start-2)*cluster
			n := min(cluster, size-pos)
			if offset+n > total*512 {
				return nil, fmt.Errorf("FAT16 cluster outside boot partition")
			}
			if _, err := r.ReadAt(out[pos:pos+n], offset); err != nil {
				return nil, err
			}
			pos += n
			if progress != nil {
				progress(pos, size)
			}
			start = int64(binary.LittleEndian.Uint16(fat[start*2:]))
		}
		return out, nil
	}
	return nil, fmt.Errorf("image boot partition lacks %s; installer-enabled image required", name)
}
