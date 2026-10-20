package nanokvm

import (
	"strings"
	"testing"
)

func TestBootTokenBinding(t *testing.T) {
	env := []byte("bootfile=boot.sd\nothbootargs=earlycon=sbi\nsdboot=bootm\n\x00")
	b, err := bindEnvironment(env, strings.Repeat("a", 64))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "earlycon=sbi onekvm.installer="+strings.Repeat("a", 64)+"\n") || b[len(b)-1] != 0 {
		t.Fatal("lost boot token or NUL terminator")
	}
	for _, bad := range []string{"bootfile=boot.sd\n", "othbootargs=x\nothbootargs=y\n", "othbootargs=x onekvm.installer=old\n"} {
		if _, err := bindEnvironment([]byte(bad), "new"); err == nil {
			t.Fatal("accepted ambiguous boot environment")
		}
	}
}
