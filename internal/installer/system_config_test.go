package installer

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func configFixture() []byte {
	return []byte(`{"schema_version":17,"auth":{"new_protection":true,"users":[{"username":"admin","role":"admin","password_hash":"private-password-hash","totp":{"secret":"private-mfa-secret"},"backup_codes":["private-recovery-code"],"plugins":["removed-extension"]}]},"network":{"device":"eth0","ipv4_mode":"static","ipv4_address":"10.0.0.7/24","hostname":"kept-host","wifi":{"private":"kept-wifi-credential"}},"system":{"timezone":"Asia/Shanghai"},"video":{"future_setting":42},"ssh":{"authorized_keys":["kept-public-key"]}}`)
}

func TestSnapshotSummarizesManagedInterfacesAndKeepsSystemTimezone(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "etc/onekvm"), 0700); err != nil {
		t.Fatal(err)
	}
	data := []byte(`{"schema_version":19,"auth":{"users":[{"username":"admin"}]},"network":{"primary_interface":"eth0","hostname":"kept-host","interfaces":[{"name":"wlan0","ipv4_mode":"dhcp"},{"name":"eth0","ipv4_mode":"static","ipv4_address":"10.100.99.107/24"}]},"system":{"timezone":""}}`)
	if err := os.WriteFile(filepath.Join(root, "etc/onekvm/config.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "etc/timezone"), []byte("Universal\n"), 0644); err != nil {
		t.Fatal(err)
	}
	snapshot, err := SnapshotSystem(root)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Access.Network.Interface != "eth0" || snapshot.Access.Network.Address != "10.100.99.107/24" || snapshot.Timezone != "Universal" {
		t.Fatal("lost current access information", snapshot.Access, snapshot.Timezone)
	}
}

func TestRecoveryLocatesStageInsideUserdataBindRoot(t *testing.T) {
	d := Device{DataMount: "/mnt/storage", DataRoot: "/storage"}
	recovery := recoveryStagingDevice(d, "/run/data")
	if StageDir(recovery) != filepath.Join("/run/data/storage", StageName) {
		t.Fatal("recovery lost the userdata bind root", StageDir(recovery))
	}
	if !SameDisk(d, recovery) {
		t.Fatal("recovery mountpoint changed disk identity")
	}
	changed := d
	changed.DataRoot = "/other"
	if SameDisk(d, changed) {
		t.Fatal("accepted another staging root")
	}
}

func TestSnapshotKeepsWebAccessProtocolAndPort(t *testing.T) {
	for _, test := range []struct {
		json, scheme string
		port         int
	}{
		{`"tls_enabled":false,"http_port":8080`, "http", 8080},
		{`"tls_enabled":true,"https_port":8443`, "https", 8443},
	} {
		data := bytes.Replace(configFixture(), []byte(`"network":{`), []byte(`"network":{`+test.json+`,`), 1)
		_, access, _, err := systemConfiguration(data)
		if err != nil || access.WebScheme != test.scheme || access.WebPort != test.port {
			t.Fatal("lost original Web address", access, err)
		}
	}
}

func TestSystemSnapshotKeepsAllSystemFieldsAndRemovesExtensionBindings(t *testing.T) {
	source := configFixture()
	data, access, timezone, err := systemConfiguration(source)
	if err != nil {
		t.Fatal(err)
	}
	defer clear(data)
	for _, s := range []string{"private-password-hash", "private-mfa-secret", "private-recovery-code", "kept-wifi-credential", "future_setting", "new_protection", "kept-public-key"} {
		if !bytes.Contains(data, []byte(s)) {
			t.Fatal("lost system configuration field", s)
		}
	}
	if bytes.Contains(data, []byte("removed-extension")) {
		t.Fatal("retained extension bindings")
	}
	if access.Username != "admin" || access.Network.Address != "10.0.0.7/24" || timezone != "Asia/Shanghai" {
		t.Fatal("wrong nonsecret access summary")
	}
	root := t.TempDir()
	putFile := func(name string, data []byte) {
		path := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	putFile("etc/onekvm/config.json", source)
	putFile("etc/os-release", []byte("VERSION_ID=0.2.0\n"))
	putFile("etc/systemd/system/sockets.target.wants/dropbear.socket", nil)
	snapshot, err := SnapshotSystem(root)
	if err != nil {
		t.Fatal(err)
	}
	defer clear(snapshot.Data)
	if !snapshot.SSHEnabled || snapshot.Version != "0.2.0" {
		t.Fatal("lost SSH policy or source version")
	}
	var old, new map[string]any
	if err := json.Unmarshal(source, &old); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(snapshot.Data, &new); err != nil {
		t.Fatal(err)
	}
	delete(old["auth"].(map[string]any)["users"].([]any)[0].(map[string]any), "plugins")
	a, _ := json.Marshal(old)
	b, _ := json.Marshal(new)
	if !bytes.Equal(a, b) {
		t.Fatal("changed system fields")
	}
}

func TestConfigurationMigrationRejectsDowngradesAndTampering(t *testing.T) {
	for _, version := range []string{"0.1.9", "unknown"} {
		if checkConfigVersion("0.2.0", Image{BuildID: version + "-machine-r1"}) == nil {
			t.Fatal("accepted downgrade or unknown version")
		}
	}
	for _, version := range []string{"0.2.0", "0.2.1", "1.0.0"} {
		if err := checkConfigVersion("0.2.0", Image{BuildID: version + "-machine-r1"}); err != nil {
			t.Fatal(err)
		}
	}
	dir := t.TempDir()
	path := filepath.Join(dir, SystemConfigName)
	data := configFixture()
	hash := sha256.Sum256(data)
	if err := WritePrivate(path, data); err != nil {
		t.Fatal(err)
	}
	if _, err := loadSystemConfig(dir, hex.EncodeToString(hash[:])); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("changed"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadSystemConfig(dir, hex.EncodeToString(hash[:])); err == nil {
		t.Fatal("accepted changed snapshot")
	}
}

func archiveFiles(t *testing.T, archive []byte) map[string][]byte {
	t.Helper()
	z, err := gzip.NewReader(bytes.NewReader(archive))
	if err != nil {
		t.Fatal(err)
	}
	defer z.Close()
	raw, err := io.ReadAll(z)
	if err != nil {
		t.Fatal(err)
	}
	result := map[string][]byte{}
	for pos := 0; pos < len(raw); {
		if len(raw)-pos < 110 || string(raw[pos:pos+6]) != "070701" {
			t.Fatal("invalid archive")
		}
		field := func(i int) int {
			n, err := strconv.ParseUint(string(raw[pos+i:pos+i+8]), 16, 32)
			if err != nil {
				t.Fatal(err)
			}
			return int(n)
		}
		size, nameSize := field(54), field(94)
		name := string(raw[pos+110 : pos+110+nameSize-1])
		start := (pos + 110 + nameSize + 3) &^ 3
		result[name] = bytes.Clone(raw[start : start+size])
		pos = (start + size + 3) &^ 3
	}
	return result
}

func TestConfigurationHookRestoresOnceAndRemovesPrivateSeed(t *testing.T) {
	data := configFixture()
	hash := sha256.Sum256(data)
	archive, err := ConfigurationHook(Request{ConfigSHA256: hex.EncodeToString(hash[:]), SSHEnabled: true, Timezone: "Asia/Shanghai"})
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(archive, []byte("private-mfa-secret")) {
		t.Fatal("embedded credentials in boot hook")
	}
	script := string(archiveFiles(t, archive)["init.d/18-onekvmrestore"])
	for _, test := range []struct {
		slot    string
		corrupt bool
	}{{"", false}, {"", true}, {"A", false}, {"B", false}, {"invalid", false}} {
		t.Run(test.slot+strconv.FormatBool(test.corrupt), func(t *testing.T) {
			root := t.TempDir()
			boot := filepath.Join(root, "boot")
			dest := filepath.Join(root, "data")
			if err := os.MkdirAll(boot, 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.MkdirAll(dest, 0700); err != nil {
				t.Fatal(err)
			}
			seed := filepath.Join(boot, SystemConfigName)
			input := data
			if test.corrupt {
				input = []byte("corrupted")
			}
			if err := os.WriteFile(seed, input, 0600); err != nil {
				t.Fatal(err)
			}
			body := strings.ReplaceAll(script, ". /usr/lib/onekvm/storage-layout.sh", ":")
			body = strings.ReplaceAll(body, "/run/onekvm-config-boot", boot)
			body = strings.ReplaceAll(body, "/run/onekvm-config-data", dest)
			body = "bootparam_overlayrootisolated=" + cliQuote(test.slot) + "\nmount() { :; }; umount() { :; }; sync() { :; }; onekvm_storage_select() { :; }; fatal() { exit 25; };\n" + body + "\nonekvmrestore_run\n"
			cmd := exec.Command("sh", "-c", body)
			out, err := cmd.CombinedOutput()
			upper := filepath.Join(dest, "upper")
			if test.slot != "" {
				upper = filepath.Join(dest, "slots", test.slot, "upper")
			}
			path := filepath.Join(upper, "etc/onekvm/config.json")
			if test.corrupt || test.slot == "invalid" {
				if err == nil {
					t.Fatal("accepted corrupt seed")
				}
				if _, e := os.Stat(path); !os.IsNotExist(e) {
					t.Fatal("saved corrupt configuration")
				}
				return
			}
			if err != nil {
				t.Fatalf("hook failed: %v %s", err, out)
			}
			got, err := os.ReadFile(path)
			if err != nil || !bytes.Equal(data, got) {
				t.Fatal("config was not restored exactly", err)
			}
			stat, err := os.Stat(path)
			if err != nil || stat.Mode().Perm() != 0600 {
				t.Fatal("config is not private", err)
			}
			for _, directory := range []string{".", "etc", "etc/onekvm", "etc/systemd", "etc/systemd/system", "etc/systemd/system/sockets.target.wants"} {
				info, err := os.Stat(filepath.Join(upper, directory))
				if err != nil || info.Mode().Perm() != 0755 {
					t.Fatal("system directory is inaccessible to unprivileged services", directory, err)
				}
			}
			if _, err := os.Stat(seed); !os.IsNotExist(err) {
				t.Fatal("retained private seed")
			}
			if _, err := exec.Command("sh", "-c", body).CombinedOutput(); err != nil {
				t.Fatal("replayed completed import", err)
			}
			link, err := os.Readlink(filepath.Join(upper, "etc/systemd/system/sockets.target.wants/dropbear.socket"))
			if err != nil || link != "/usr/lib/systemd/system/dropbear.socket" {
				t.Fatal("SSH startup not restored", err)
			}
		})
	}
}
