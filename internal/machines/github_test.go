package machines_test

import (
	"testing"

	. "github.com/onekvm/onekvm-installer/internal/installer"
	_ "github.com/onekvm/onekvm-installer/internal/machines"
)

func TestAssetMatching(t *testing.T) {
	r := Release{Assets: []ReleaseAsset{{Name: "onekvm-nanokvm-1.img"}, {Name: "onekvm-nanokvm-pro-sd-1.img"}, {Name: "onekvm-nanokvm-pro-emmc-1.img"}, {Name: "onekvm-nanokvm-1.img.gz"}, {Name: "onekvm-nanokvm-1.img.xz"}, {Name: "onekvm-nanokvm-pro-sd-1.img.gz"}, {Name: "onekvm-nanokvm-pro-emmc-1.img.xz"}, {Name: "onekvm-nanokvm-1.fwup"}, {Name: "onekvm-nanokvm-1.img.sha256"}}}
	for _, p := range []string{"nanokvm", "pro-sd", "pro-emmc"} {
		want := 1
		if _, err := MachineFor(p); err != nil {
			want = 0
		}
		if a := ImageAssets(r, p); len(a) != want {
			t.Fatal(p, a)
		}
	}
	if a := ImageAssets(r, "unknown"); len(a) != 0 {
		t.Fatal("matched unknown machine")
	}
}
