package nanokvm

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/onekvm/onekvm-installer/internal/installer"
)

func (Machine) PrepareReinstall(image, dir string, req installer.Request, p installer.Progress) error {
	if err := extractBootstrapFor(image, dir, p, true); err != nil {
		return err
	}
	stagedFIP, err := installer.HashFile(filepath.Join(dir, "fip.bin"), nil)
	if err != nil {
		return err
	}
	currentFIP, err := installer.HashFile(filepath.Join(req.Device.BootMount, "fip.bin"), nil)
	if err != nil {
		return err
	}
	if stagedFIP != currentFIP {
		return fmt.Errorf("reinstallation requires an image with the same boot firmware; refusing to replace U-Boot")
	}
	ramdisk, err := installer.RecoveryInitramfs()
	if err != nil {
		return err
	}
	path := filepath.Join(dir, "kernel.itb")
	fit, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	fit, err = replaceFITRamdisk(fit, ramdisk)
	if err != nil {
		return err
	}
	return installer.WritePrivate(path, fit)
}

// Preserve the verified kernel, DTB and configuration while replacing the
// inline ramdisk and its hash. External data and signed FITs are not rewritten.
func replaceFITRamdisk(fit, ramdisk []byte) ([]byte, error) {
	return rewriteFITRamdisk(fit, func([]byte) []byte { return ramdisk })
}
func appendFITInitramfs(fit, extra []byte) ([]byte, error) {
	return rewriteFITRamdisk(fit, func(old []byte) []byte { return append(append(bytes.Clone(old), 0, 0, 0, 0), extra...) })
}
func rewriteFITRamdisk(fit []byte, replace func([]byte) []byte) ([]byte, error) {
	invalid := func() ([]byte, error) { return nil, fmt.Errorf("unsupported NanoKVM recovery FIT") }
	if len(fit) < 40 || binary.BigEndian.Uint32(fit) != 0xd00dfeed {
		return invalid()
	}
	u32 := func(i int) int { return int(binary.BigEndian.Uint32(fit[i:])) }
	total, start, names, nameSize, structSize := u32(4), u32(8), u32(12), u32(32), u32(36)
	if total > len(fit) || start < 40 || structSize < 4 || start > total-structSize || names < start+structSize || nameSize < 1 || names > total-nameSize {
		return invalid()
	}
	stringsBlock := fit[names : names+nameSize]
	var out bytes.Buffer
	var stack []string
	dataCount, hashCount, compressionCount := 0, 0, 0
	put := func(v uint32) { _ = binary.Write(&out, binary.BigEndian, v) }
	var hash [32]byte
	ended := false
	for pos, end := start, start+structSize; pos < end; {
		if pos+4 > end {
			return invalid()
		}
		token := u32(pos)
		pos += 4
		put(uint32(token))
		switch token {
		case 1:
			n := bytes.IndexByte(fit[pos:end], 0)
			if n < 0 {
				return invalid()
			}
			name := string(fit[pos : pos+n])
			if strings.HasPrefix(name, "signature") {
				return invalid()
			}
			stack = append(stack, name)
			next := (pos + n + 1 + 3) &^ 3
			if next > end {
				return invalid()
			}
			out.Write(fit[pos:next])
			pos = next
		case 2:
			if len(stack) == 0 {
				return invalid()
			}
			stack = stack[:len(stack)-1]
		case 3:
			if pos+8 > end {
				return invalid()
			}
			size, offset := u32(pos), u32(pos+4)
			pos += 8
			if offset >= nameSize || size > end-pos {
				return invalid()
			}
			n := bytes.IndexByte(stringsBlock[offset:], 0)
			if n < 0 {
				return invalid()
			}
			name := string(stringsBlock[offset : offset+n])
			data := fit[pos : pos+size]
			path := strings.Join(stack, "/")
			if path == "/images/ramdisk-1" {
				switch name {
				case "data":
					data = replace(data)
					hash = sha256.Sum256(data)
					dataCount++
				case "compression":
					data = []byte("none\x00")
					compressionCount++
				case "data-position", "data-offset", "data-size":
					return invalid()
				}
			}
			if strings.HasPrefix(path, "/images/ramdisk-1/hash") {
				if name == "algo" && string(data) != "sha256\x00" {
					return invalid()
				}
				if name == "value" {
					if dataCount != 1 {
						return invalid()
					}
					data = hash[:]
					hashCount++
				}
			}
			put(uint32(len(data)))
			put(uint32(offset))
			out.Write(data)
			for out.Len()%4 != 0 {
				out.WriteByte(0)
			}
			pos = (pos + size + 3) &^ 3
			if pos > end {
				return invalid()
			}
		case 4: // FDT_NOP
		case 9:
			if len(stack) != 0 {
				return invalid()
			}
			ended = true
			pos = end
		default:
			return invalid()
		}
	}
	if !ended || dataCount != 1 || hashCount != 1 || compressionCount != 1 {
		return invalid()
	}
	result := append(bytes.Clone(fit[:start]), out.Bytes()...)
	newNames := len(result)
	result = append(result, stringsBlock...)
	if len(result) >= 16*installer.MiB {
		return nil, fmt.Errorf("recovery FIT exceeds the 16 MiB boot window")
	}
	binary.BigEndian.PutUint32(result[4:], uint32(len(result)))
	binary.BigEndian.PutUint32(result[12:], uint32(newNames))
	binary.BigEndian.PutUint32(result[36:], uint32(out.Len()))
	return result, nil
}

func (Machine) SeedConfiguration(boot string, d installer.Device, req installer.Request, config, hook []byte) error {
	for _, name := range []string{"boot.sd", "boot-b.itb"} {
		path := filepath.Join(boot, name)
		fit, err := os.ReadFile(path)
		if os.IsNotExist(err) && name == "boot-b.itb" {
			continue
		}
		if err != nil {
			return err
		}
		fit, err = appendFITInitramfs(fit, hook)
		if err != nil {
			return err
		}
		if err := installer.RewriteBootPayload(path, fit); err != nil {
			return err
		}
	}
	return installer.WritePrivate(filepath.Join(boot, installer.SystemConfigName), config)
}
