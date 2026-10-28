package nanokvm

import (
	"encoding/binary"
	"fmt"
	"io"
	"os"

	"github.com/onekvm/onekvm-installer/internal/installer"
)

func extractBootstrap(image, dir string, progress installer.Progress) error {
	r, err := os.Open(image)
	if err != nil {
		return err
	}
	defer r.Close()
	mbr := make([]byte, 512)
	if _, err = r.ReadAt(mbr, 0); err != nil {
		return err
	}
	start := int64(binary.LittleEndian.Uint32(mbr[454:]))
	sectors := int64(binary.LittleEndian.Uint32(mbr[458:]))
	if start != 1 || sectors < 32768 || sectors > 262144 || sectors%2048 != 0 {
		return fmt.Errorf("unsupported bootstrap partition")
	}
	f := io.NewSectionReader(r, start*512, sectors*512)
	header := make([]byte, 512)
	if _, err = f.ReadAt(header, 0); err != nil {
		return err
	}
	total := int64(binary.LittleEndian.Uint16(header[19:]))
	if total == 0 {
		total = int64(binary.LittleEndian.Uint32(header[32:]))
	}
	if total != sectors {
		return fmt.Errorf("boot filesystem does not match carrier partition")
	}
	kernel, err := installer.FatFileProgress(f, "BOOT    SD ", 16*installer.MiB, progress)
	if err != nil {
		return err
	}
	if len(kernel) < 4 || binary.BigEndian.Uint32(kernel[:4]) != 0xd00dfeed {
		return fmt.Errorf("bootstrap kernel is not a FIT image")
	}
	if err = installer.WritePrivate(dir+"/kernel.itb", kernel); err != nil {
		return err
	}
	fip, err := installer.FatFile(f, "FIP     BIN", 2*installer.MiB)
	if err != nil {
		return err
	}
	if err = installer.WritePrivate(dir+"/fip.bin", fip); err != nil {
		return err
	}
	env, err := installer.FatFile(f, "UENV    TXT", 16384)
	if err != nil {
		return err
	}
	return installer.WritePrivate(dir+"/uEnv.txt", env)
}
