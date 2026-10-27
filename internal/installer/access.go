package installer

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode"
)

// AccessInfo contains only the nonsecret settings needed to log in after
// installation. It is derived from firstboot.txt, not added to the recovery
// request format, so existing recovery images remain compatible.
type AccessInfo struct {
	Username  string
	Network   Network
	WebScheme string
	WebPort   int
}

func preparedAccess(dir, expectedHash string) (AccessInfo, error) {
	var access AccessInfo
	b, err := os.ReadFile(filepath.Join(dir, "firstboot.txt"))
	if err != nil {
		return access, err
	}
	defer clear(b)
	hash := sha256.Sum256(b)
	if len(b) > 65536 || hex.EncodeToString(hash[:]) != expectedHash {
		return access, fmt.Errorf("staged firstboot configuration changed")
	}
	return AccessFromFirstboot(b)
}

func AccessFromFirstboot(b []byte) (AccessInfo, error) {
	var access AccessInfo
	if len(b) > 65536 {
		return access, fmt.Errorf("oversized firstboot configuration")
	}
	for _, line := range strings.Split(string(b), "\n") {
		switch {
		case strings.HasPrefix(line, "set auth user ") && strings.HasSuffix(line, " role admin"):
			quoted := strings.TrimSuffix(strings.TrimPrefix(line, "set auth user "), " role admin")
			if len(quoted) < 2 || quoted[0] != '\'' || quoted[len(quoted)-1] != '\'' {
				return AccessInfo{}, fmt.Errorf("invalid staged administrator name")
			}
			// Decode only Firstboot's exact quoting format; never evaluate CLI
			// commands or parse the encrypted-password line into the summary.
			name := strings.ReplaceAll(quoted[1:len(quoted)-1], `'\''`, "'")
			if cliQuote(name) != quoted || len(name) == 0 || len(name) > 64 || strings.TrimSpace(name) != name || strings.ContainsFunc(name, unicode.IsControl) {
				return AccessInfo{}, fmt.Errorf("invalid staged administrator name")
			}
			access.Username = name
		case strings.HasPrefix(line, "set interfaces ethernet "):
			v := strings.Fields(line)
			if len(v) == 6 {
				switch v[4] {
				case "mode":
					access.Network.Interface, access.Network.Mode = v[3], v[5]
				case "address":
					access.Network.Address = v[5]
				}
			}
		case strings.HasPrefix(line, "set network hostname "):
			access.Network.Hostname = strings.TrimPrefix(line, "set network hostname ")
		}
	}
	if access.Username == "" || access.Network.Validate() != nil {
		return AccessInfo{}, fmt.Errorf("invalid staged login or network settings")
	}
	return access, nil
}
