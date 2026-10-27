package installer

import (
	"encoding/binary"
	"os"
	"testing"
)

func TestFAT16ExtractionAndCycle(t *testing.T) {
	f, err := os.CreateTemp(t.TempDir(), "boot.fat")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	_ = f.Truncate(36 * MiB)
	header := make([]byte, 512)
	binary.LittleEndian.PutUint16(header[11:], 512)
	header[13] = 8
	binary.LittleEndian.PutUint16(header[14:], 1)
	header[16] = 2
	binary.LittleEndian.PutUint16(header[17:], 512)
	binary.LittleEndian.PutUint16(header[22:], 256)
	binary.LittleEndian.PutUint32(header[32:], 73728)
	header[510] = 0x55
	header[511] = 0xaa
	_, _ = f.WriteAt(header, 0)
	fat := make([]byte, 131072)
	binary.LittleEndian.PutUint16(fat[4:], 0xffff)
	_, _ = f.WriteAt(fat, 512)
	root := make([]byte, 16384)
	copy(root, "INSTAPI    ")
	binary.LittleEndian.PutUint16(root[26:], 2)
	binary.LittleEndian.PutUint32(root[28:], 2)
	_, _ = f.WriteAt(root, 512+2*131072)
	_, _ = f.WriteAt([]byte("1\n"), 512+2*131072+16384)
	b, err := FatFile(f, "INSTAPI    ", 16)
	if err != nil || string(b) != "1\n" {
		t.Fatal(string(b), err)
	}
	free, err := FatFreeBytes(f)
	clusters := (36*MiB - 512 - 2*131072 - 16384) / 4096
	if err != nil || free != int64(clusters-1)*4096 {
		t.Fatal("incorrect carrier free space", free, err)
	}
	if _, err = FatFile(f, "MISSING    ", 16); err == nil {
		t.Fatal("accepted missing file")
	}
	binary.LittleEndian.PutUint32(root[28:], 8192)
	_, _ = f.WriteAt(root, 512+2*131072)
	binary.LittleEndian.PutUint16(fat[4:], 2)
	_, _ = f.WriteAt(fat, 512)
	if _, err = FatFile(f, "INSTAPI    ", 8192); err == nil {
		t.Fatal("followed cyclic cluster chain")
	}
	for i := range fat {
		fat[i] = 0xff
	}
	_, _ = f.WriteAt(fat, 512)
	if free, err := FatFreeBytes(f); err != nil || free != 0 {
		t.Fatal("full FAT reported free restoration space", free, err)
	}
}

func TestFAT16LongNamesAndInvalidSequences(t *testing.T) {
	for _, bad := range []string{"", "sequence", "checksum", "reserved"} {
		t.Run(bad, func(t *testing.T) {
			f, err := os.CreateTemp(t.TempDir(), "boot.fat")
			if err != nil {
				t.Fatal(err)
			}
			defer f.Close()
			f.Truncate(16 * MiB)
			header := make([]byte, 512)
			binary.LittleEndian.PutUint16(header[11:], 512)
			header[13], header[16] = 8, 2
			binary.LittleEndian.PutUint16(header[14:], 1)
			binary.LittleEndian.PutUint16(header[17:], 512)
			binary.LittleEndian.PutUint16(header[19:], 32768)
			binary.LittleEndian.PutUint16(header[22:], 32)
			header[510], header[511] = 0x55, 0xaa
			f.WriteAt(header, 0)
			fat := make([]byte, 16384)
			binary.LittleEndian.PutUint16(fat[4:], 0xffff)
			f.WriteAt(fat, 512)
			dir := make([]byte, 16384)
			short := []byte("INITRA~1UIM")
			var checksum byte
			for _, c := range short {
				checksum = (checksum >> 1) | (checksum << 7)
				checksum += c
			}
			name := []rune("initramfs-a.uimg")
			for seq := 2; seq >= 1; seq-- {
				e := dir[(2-seq)*32 : (3-seq)*32]
				e[0], e[11], e[13] = byte(seq), 0xf, checksum
				if seq == 2 {
					e[0] |= 0x40
				}
				offsets := []int{1, 3, 5, 7, 9, 14, 16, 18, 20, 22, 24, 28, 30}
				for i, offset := range offsets {
					at := (seq-1)*13 + i
					value := uint16(0xffff)
					if at < len(name) {
						value = uint16(name[at])
					} else if at == len(name) {
						value = 0
					}
					binary.LittleEndian.PutUint16(e[offset:], value)
				}
			}
			copy(dir[64:], short)
			binary.LittleEndian.PutUint16(dir[90:], 2)
			binary.LittleEndian.PutUint32(dir[92:], 7)
			switch bad {
			case "sequence":
				dir[32] = 2
			case "checksum":
				dir[45]++
			case "reserved":
				dir[12] = 1
			}
			f.WriteAt(dir, 512+2*16384)
			f.WriteAt([]byte("ramdisk"), 512+2*16384+16384)
			got, err := FatFile(f, "initramfs-a.uimg", 100)
			if bad != "" {
				if err == nil {
					t.Fatal("accepted malformed LFN")
				}
				return
			}
			if err != nil || string(got) != "ramdisk" {
				t.Fatal(string(got), err)
			}
		})
	}
}
