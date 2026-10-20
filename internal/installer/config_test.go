package installer

import (
	"strings"
	"testing"

	"golang.org/x/crypto/bcrypt"
)

func TestFirstbootHashedPasswordAndQuoting(t *testing.T) {
	password := "quote'|test-secret"
	b, err := Firstboot("operator", password, Network{Interface: "eth0", Mode: "dhcp"}, SystemSettings{})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), password) || !strings.Contains(string(b), "encrypted-password '$2a$") || !strings.HasSuffix(string(b), "request system setup complete\n") {
		t.Fatal(string(b))
	}
	line := strings.Split(string(b), "\n")[1]
	hash := strings.TrimSuffix(strings.Split(line, "encrypted-password '")[1], "'")
	if err = bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)); err != nil {
		t.Fatal(err)
	}
	if cliQuote("a'|b") != `'a'\''|b'` {
		t.Fatal("incorrect CLI quoting")
	}
}
func TestNetworkCopyPreservesDHCP(t *testing.T) {
	r := t.TempDir()
	put(t, r, "etc/network/interfaces", "auto eth0\niface eth0 inet dhcp\n")
	n, err := CopyNetwork(r, "eth0")
	if err != nil || n.Mode != "dhcp" || n.Address != "" {
		t.Fatal(n, err)
	}
	put(t, r, "etc/network/interfaces", "auto lo\niface lo inet loopback\n")
	put(t, r, "proc/467/cmdline", "udhcpc\x00-i\x00eth0\x00-b\x00")
	n, err = CopyNetwork(r, "eth0")
	if err != nil || n.Mode != "dhcp" {
		t.Fatal(n, err)
	}
}
func TestUnknownNetworkRequiresConfiguration(t *testing.T) {
	if _, err := CopyNetwork(t.TempDir(), "eth0"); err == nil {
		t.Fatal("guessed unknown network")
	}
	for _, n := range []Network{{Interface: "eth0", Mode: "static", Address: "192.168.1.5/24", Gateway: "10.0.0.1"}, {Interface: "wlan0", Mode: "dhcp"}} {
		if n.Validate() == nil {
			t.Fatal("accepted unsupported network", n)
		}
	}
}

func TestFirstbootDNSStatements(t *testing.T) {
	b, err := Firstboot("operator", "testing-only", Network{Interface: "eth0", Mode: "dhcp", DNS: []string{"1.1.1.1", "8.8.8.8"}}, SystemSettings{})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(b), "set system name-server ") != 2 {
		t.Fatal("DNS must use one CLI statement per address")
	}
}

func TestHostnameDefaultsAndCopy(t *testing.T) {
	r := t.TempDir()
	put(t, r, "etc/network/interfaces", "auto eth0\niface eth0 inet dhcp\n")
	put(t, r, "etc/hostname", "stored-name\n")
	if got := CurrentHostname(r); got != "stored-name" {
		t.Fatal(got)
	}
	put(t, r, "proc/sys/kernel/hostname", "active-name\n")
	n, err := CopyNetwork(r, "eth0")
	if err != nil || n.Hostname != "active-name" {
		t.Fatal(n, err)
	}
}

func TestFirstbootCountsPasswordCharacters(t *testing.T) {
	n := Network{Interface: "eth0", Mode: "dhcp"}
	if _, err := Firstboot("operator", "一二三四", n, SystemSettings{}); err == nil {
		t.Fatal("accepted fewer than eight characters")
	}
	password := "一二三四五六七八"
	b, err := Firstboot("operator", password, n, SystemSettings{})
	if err != nil {
		t.Fatal(err)
	}
	line := strings.Split(string(b), "\n")[1]
	hash := strings.TrimSuffix(strings.Split(line, "encrypted-password '")[1], "'")
	if err := bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)); err != nil {
		t.Fatal(err)
	}
	if _, err := Firstboot("operator", strings.Repeat("密", 25), n, SystemSettings{}); err == nil {
		t.Fatal("exceeded the bcrypt input limit")
	}
}
