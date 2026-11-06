package installer

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestFirstbootNetworkPolicyRoundTripAndValidation(t *testing.T) {
	policy := &NetworkPolicy{Version: 1, Disabled: []string{"internet", "ntp", "dns"}}
	data, err := Firstboot("admin", "a-valid-password", Network{Mode: "dhcp", Interface: "eth0", Hostname: "lab"}, SystemSettings{Language: "en", Timezone: "UTC", NetworkPolicy: policy})
	if err != nil {
		t.Fatal(err)
	}
	got, err := ParseNetworkPolicy(data)
	if err != nil || !reflect.DeepEqual(got, policy) {
		t.Fatal("policy lost in firstboot", got, err)
	}
	for _, raw := range []string{`{"version":2,"disabled":[]}`, `{"version":1,"disabled":["cpu"]}`, `{"version":1,"disabled":["dns","dns"]}`, `{"version":1,"disabled":[],"unknown":true}`, `{"version":1,"disabled":[]} {}`, `{"version":1}`, `{"version":1,"disabled":null}`} {
		if _, err := ParseNetworkPolicy([]byte(NetworkPolicyPrefix + raw)); err == nil {
			t.Fatal("accepted", raw)
		}
	}
	if _, err := ParseNetworkPolicy(append(data, data...)); err == nil {
		t.Fatal("accepted duplicate policy")
	}
	if CheckNetworkPolicy(Image{}, policy) == nil {
		t.Fatal("legacy image silently accepted restrictions")
	}
	if err := CheckNetworkPolicy(Image{NetworkPolicy: true}, policy); err != nil {
		t.Fatal(err)
	}
	if err := CheckNetworkPolicy(Image{}, &NetworkPolicy{Version: 1, Disabled: []string{}}); err != nil {
		t.Fatal(err)
	}
}

func TestImageNetworkPolicyCapabilityFlag(t *testing.T) {
	path := fixtureImage(t)
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	st, _ := f.Stat()
	tail := make([]byte, 512)
	if _, err := f.ReadAt(tail, st.Size()-512); err != nil {
		t.Fatal(err)
	}
	binary.LittleEndian.PutUint16(tail[22:], 1)
	image, err := parseMetadata(tail, st.Size())
	if err != nil || !image.NetworkPolicy {
		t.Fatal("capability not read", err)
	}
	binary.LittleEndian.PutUint16(tail[22:], 3)
	if _, err := parseMetadata(tail, st.Size()); err == nil {
		t.Fatal("accepted unknown flag")
	}
}

func TestSnapshotPreservesAndValidatesPolicy(t *testing.T) {
	root := t.TempDir()
	directory := filepath.Join(root, "etc/onekvm")
	if err := os.MkdirAll(directory, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "config.json"), configFixture(), 0600); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(directory, "network-policy.json")
	if err := os.WriteFile(path, []byte(`{"version":1,"disabled":["stun"]}`), 0644); err != nil {
		t.Fatal(err)
	}
	snapshot, err := SnapshotSystem(root)
	if err != nil || snapshot.NetworkPolicy == nil || strings.Join(snapshot.NetworkPolicy.Disabled, ",") != "stun" {
		t.Fatal("lost installed policy", err)
	}
	if err := os.WriteFile(path, []byte(`{"version":1,"disabled":["typo"]}`), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := SnapshotSystem(root); err == nil {
		t.Fatal("silently ignored invalid installed policy")
	}
	req := Request{Image: Image{NetworkPolicy: true}, NetworkPolicy: snapshot.NetworkPolicy}
	stage := t.TempDir()
	if err := os.WriteFile(filepath.Join(stage, "firstboot.txt"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	if err := RequestHashes(&req, stage, true, nil); err == nil || !strings.Contains(err.Error(), "network policy does not match") {
		t.Fatal("request/firstboot mismatch accepted", err)
	}
}
