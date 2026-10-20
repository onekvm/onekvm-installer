package nanokvmpro

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"hash/crc32"
	"io"
	"os"
	"sort"
	"strings"
	"syscall"

	"github.com/onekvm/onekvm-installer/internal/installer"
)

const proEnvOffset = int64(0x4c0000)
const proEnvSize = 1024 * 1024
const proBootCommand = "run onekvm_sd_boot; run onekvm_emmc_boot; axera_boot"
const proInstallBootCommand = "run onekvm_emmc_boot; axera_boot"
const proSDBoot = "mmc dev 1; mmc rescan; mw.l 0x48000000 0 1; fatload mmc 1:1 0x48000000 boot.scr; source 0x48000000"
const proEMMCBoot = "mmc dev 0; mmc rescan; mw.l 0x48000000 0 1; mmc read 0x48000000 0x4ad80 0x80; source 0x48000000"

// The supported factory environment is nonredundant: little-endian CRC32,
// then 1 MiB - 4 bytes of NUL-delimited records. Verify it before any mutation;
// never replace an invalid environment with defaults.
func parseProEnvironment(raw []byte) (map[string]string, error) {
	if len(raw) != proEnvSize || binary.LittleEndian.Uint32(raw) != crc32.ChecksumIEEE(raw[4:]) {
		return nil, fmt.Errorf("unsupported or corrupt Pro U-Boot environment")
	}
	end := bytes.Index(raw[4:], []byte{0, 0})
	if end < 0 {
		return nil, fmt.Errorf("unterminated Pro U-Boot environment")
	}
	out := map[string]string{}
	for _, entry := range bytes.Split(raw[4:4+end], []byte{0}) {
		k, v, ok := strings.Cut(string(entry), "=")
		if !ok || k == "" {
			return nil, fmt.Errorf("invalid Pro U-Boot environment record")
		}
		if _, duplicate := out[k]; duplicate {
			return nil, fmt.Errorf("duplicate Pro U-Boot environment record")
		}
		out[k] = v
	}
	if out["bootsystem"] != "A" {
		return nil, fmt.Errorf("Pro installation requires factory slot A")
	}
	switch out["bootcmd"] {
	case "sd_update", "axera_boot", proBootCommand, proInstallBootCommand:
	default:
		return nil, fmt.Errorf("unsupported Pro boot command; refusing to overwrite custom boot policy")
	}
	return out, nil
}
func proPriorityEnvironment(raw []byte, recovery bool) ([]byte, error) {
	entries, err := parseProEnvironment(raw)
	if err != nil {
		return nil, err
	}
	entries["bootcmd"] = proBootCommand
	if recovery {
		entries["bootcmd"] = proInstallBootCommand
	}
	entries["onekvm_sd_boot"] = proSDBoot
	entries["onekvm_emmc_boot"] = proEMMCBoot
	keys := make([]string, 0, len(entries))
	for key := range entries {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	data := make([]byte, 4, proEnvSize)
	for _, key := range keys {
		data = append(data, []byte(key+"="+entries[key])...)
		data = append(data, 0)
	}
	data = append(data, 0)
	if len(data) > proEnvSize {
		return nil, fmt.Errorf("Pro boot environment has no free space")
	}
	data = append(data, make([]byte, proEnvSize-len(data))...)
	binary.LittleEndian.PutUint32(data, crc32.ChecksumIEEE(data[4:]))
	return data, nil
}
func proReadEnvironment() (raw []byte, err error) {
	f, err := os.Open("/dev/mmcblk0")
	if err != nil {
		return nil, err
	}
	defer f.Close()
	raw = make([]byte, proEnvSize)
	_, err = f.ReadAt(raw, proEnvOffset)
	if err == nil {
		_, err = parseProEnvironment(raw)
	}
	return raw, err
}
func proSavePriority(d installer.Device, expected []byte) error {
	return proSavePolicy(d, expected, false)
}
func proSavePolicy(d installer.Device, expected []byte, recovery bool) error {
	if err := checkProDisk(d); err != nil {
		return err
	}
	f, err := os.OpenFile("/dev/mmcblk0", os.O_RDWR, 0)
	if err != nil {
		return err
	}
	defer f.Close()
	if err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		return err
	}
	return writeProEnvironment(f, expected, recovery)
}
func writeProEnvironment(f interface {
	io.ReaderAt
	io.WriterAt
	Sync() error
}, expected []byte, recovery bool) error {
	next, err := proPriorityEnvironment(expected, recovery)
	if err != nil {
		return err
	}
	current := make([]byte, proEnvSize)
	if _, err = f.ReadAt(current, proEnvOffset); err != nil {
		return err
	}
	if !bytes.Equal(current, expected) {
		return fmt.Errorf("Pro boot environment changed during installation; retry before reboot")
	}
	if bytes.Equal(current, next) {
		return nil
	}
	n, err := f.WriteAt(next, proEnvOffset)
	if err != nil {
		return err
	}
	if n != len(next) {
		return io.ErrShortWrite
	}
	if err = f.Sync(); err != nil {
		return err
	}
	if _, err = f.ReadAt(current, proEnvOffset); err != nil {
		return err
	}
	if !bytes.Equal(current, next) {
		return fmt.Errorf("Pro boot environment readback mismatch")
	}
	return nil
}
