//go:build riscv64 || onekvm_test

package machines

import (
	"github.com/onekvm/onekvm-installer/internal/installer"
	"github.com/onekvm/onekvm-installer/internal/machines/nanokvm"
)

func init() { installer.RegisterMachine(nanokvm.Machine{}) }
