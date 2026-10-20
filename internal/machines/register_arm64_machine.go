//go:build arm64 || onekvm_test

package machines

import (
	"github.com/onekvm/onekvm-installer/internal/installer"
	"github.com/onekvm/onekvm-installer/internal/machines/nanokvmpro"
)

func init() {
	for _, m := range nanokvmpro.Machines() {
		installer.RegisterMachine(m)
	}
}
