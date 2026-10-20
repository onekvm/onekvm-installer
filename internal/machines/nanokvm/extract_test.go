package nanokvm

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/onekvm/onekvm-installer/internal/installer"
)

func TestExtractCompactBootstrap(t *testing.T) {
	for _, invalid := range []string{"", "missing-marker", "size-mismatch"} {
		t.Run(invalid, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "compact.img")
			f, err := os.Create(path)
			if err != nil {
				t.Fatal(err)
			}
			defer f.Close()
			if err := f.Truncate(37 * installer.MiB); err != nil {
				t.Fatal(err)
			}
			mbr := make([]byte, 512)
			binary.LittleEndian.PutUint32(mbr[454:], 1)
			binary.LittleEndian.PutUint32(mbr[458:], 36*2048)
			_, _ = f.WriteAt(mbr, 0)
			header := make([]byte, 512)
			binary.LittleEndian.PutUint16(header[11:], 512)
			header[13], header[16] = 8, 2
			binary.LittleEndian.PutUint16(header[14:], 1)
			binary.LittleEndian.PutUint16(header[17:], 512)
			binary.LittleEndian.PutUint16(header[22:], 256)
			sectors := uint32(36 * 2048)
			if invalid == "size-mismatch" {
				sectors = 128 * 2048
			}
			binary.LittleEndian.PutUint32(header[32:], sectors)
			header[510], header[511] = 0x55, 0xaa
			_, _ = f.WriteAt(header, 512)
			fat, directory := make([]byte, 131072), make([]byte, 16384)
			kernel := make([]byte, 8192)
			binary.BigEndian.PutUint32(kernel, 0xd00dfeed)
			entries := []struct {
				name string
				data []byte
			}{
				{"INSTAPI    ", []byte(fmt.Sprintf("%d\n", installer.RecoveryAPI))},
				{"BOOTGROW   ", []byte("128\n")},
				{"BOOT    SD ", kernel},
				{"UENV    TXT", []byte("bootfile=boot.sd\n")},
			}
			if invalid == "missing-marker" {
				entries[1].name = "UNKNOWN    "
			}
			current := uint16(2)
			for i, entry := range entries {
				e := directory[i*32 : (i+1)*32]
				copy(e, entry.name)
				binary.LittleEndian.PutUint16(e[26:], current)
				binary.LittleEndian.PutUint32(e[28:], uint32(len(entry.data)))
				for pos := 0; pos < len(entry.data); pos += 4096 {
					n := min(4096, len(entry.data)-pos)
					offset := int64(512+512+2*131072+16384) + int64(current-2)*4096
					_, _ = f.WriteAt(entry.data[pos:pos+n], offset)
					next := uint16(0xffff)
					if pos+n < len(entry.data) {
						next = current + 1
					}
					binary.LittleEndian.PutUint16(fat[int(current)*2:], next)
					current++
				}
			}
			_, _ = f.WriteAt(fat, 1024)
			_, _ = f.WriteAt(directory, 512+512+2*131072)
			dir := t.TempDir()
			var last int64
			err = extractBootstrap(path, dir, func(done, total int64) {
				if total != int64(len(kernel)) || done < last || done > total {
					t.Errorf("invalid kernel extraction progress %d/%d", done, total)
				}
				last = done
			})
			if invalid != "" {
				if err == nil {
					t.Fatal("accepted invalid bootstrap")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			got, err := os.ReadFile(filepath.Join(dir, "kernel.itb"))
			if err != nil || !bytes.Equal(got, kernel) || last != int64(len(kernel)) {
				t.Fatal("kernel differs or incomplete progress", err)
			}
			if _, err := os.Stat(filepath.Join(dir, "boot.fat")); !os.IsNotExist(err) {
				t.Fatal("copied whole boot FAT")
			}
		})
	}
}
