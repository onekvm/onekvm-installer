package nanokvm

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"

	"github.com/onekvm/onekvm-installer/internal/installer"
)

func testFIT() []byte {
	var structure, names bytes.Buffer
	word := func(n uint32) { _ = binary.Write(&structure, binary.BigEndian, n) }
	pad := func() {
		for structure.Len()%4 != 0 {
			structure.WriteByte(0)
		}
	}
	node := func(name string) { word(1); structure.WriteString(name); structure.WriteByte(0); pad() }
	prop := func(name string, data []byte) {
		word(3)
		word(uint32(len(data)))
		word(uint32(names.Len()))
		names.WriteString(name)
		names.WriteByte(0)
		structure.Write(data)
		pad()
	}
	node("")
	node("images")
	node("kernel-1")
	prop("data", []byte("verified kernel"))
	word(2)
	node("ramdisk-1")
	prop("data", []byte("old ramdisk"))
	prop("compression", []byte("none\x00"))
	node("hash-1")
	prop("algo", []byte("sha256\x00"))
	prop("value", make([]byte, 32))
	word(2)
	word(2)
	node("fdt-1")
	prop("data", []byte("verified DTB"))
	word(2)
	word(2)
	word(2)
	word(9)
	header := make([]byte, 56)
	for off, value := range map[int]uint32{0: 0xd00dfeed, 4: uint32(56 + structure.Len() + names.Len()), 8: 56, 12: uint32(56 + structure.Len()), 16: 40, 20: 17, 24: 16, 32: uint32(names.Len()), 36: uint32(structure.Len())} {
		binary.BigEndian.PutUint32(header[off:], value)
	}
	return append(append(header, structure.Bytes()...), names.Bytes()...)
}

func TestReinstallFITPreservesKernelAndDeviceTree(t *testing.T) {
	ramdisk := []byte("new self-contained recovery")
	fit, err := replaceFITRamdisk(testFIT(), ramdisk)
	if err != nil {
		t.Fatal(err)
	}
	hash := sha256.Sum256(ramdisk)
	for _, want := range [][]byte{[]byte("verified kernel"), []byte("verified DTB"), ramdisk, hash[:]} {
		if !bytes.Contains(fit, want) {
			t.Fatal("lost verified component or ramdisk hash")
		}
	}
	if bytes.Contains(fit, []byte("old ramdisk")) {
		t.Fatal("retained old recovery")
	}
	if int(binary.BigEndian.Uint32(fit[4:])) != len(fit) {
		t.Fatal("invalid FIT total size")
	}
	if _, err := replaceFITRamdisk(fit, ramdisk); err != nil {
		t.Fatal("rewritten FIT cannot be parsed", err)
	}
	for i := 0; i < len(testFIT()); i++ {
		if _, err := replaceFITRamdisk(testFIT()[:i], ramdisk); err == nil {
			t.Fatal("accepted truncated FIT", i)
		}
	}
}

func TestReinstallRetainsBootFirmware(t *testing.T) {
	d, req := bootstrapFixture(t)
	d.Reinstall = true
	req.Device = d
	fip := filepath.Join(d.BootMount, "fip.bin")
	if err := os.WriteFile(fip, []byte("OneKVM boot firmware"), 0600); err != nil {
		t.Fatal(err)
	}
	plan, err := planBootstrap(d, req)
	if err != nil {
		t.Fatal(err)
	}
	before, err := os.Stat(fip)
	if err != nil {
		t.Fatal(err)
	}
	if err := replaceBootstrap(plan, installer.Report); err != nil {
		t.Fatal(err)
	}
	after, err := os.Stat(fip)
	if err != nil {
		t.Fatal(err)
	}
	if !os.SameFile(before, after) {
		t.Fatal("reinstallation replaced the firmware file")
	}
	if err := os.WriteFile(fip, []byte("changed firmware"), 0600); err != nil {
		t.Fatal(err)
	}
	req.State = "arming"
	if _, err := planBootstrap(d, req); err == nil {
		t.Fatal("accepted changed firmware")
	}
}
