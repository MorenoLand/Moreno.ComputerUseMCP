//go:build !windows && !linux && !darwin

package platform

import (
	"runtime"

	"github.com/MorenoLand/Moreno.ComputerUseMCP/internal/computer"
)

func newBackend() computer.Backend {
	return computer.UnsupportedBackend{Platform: runtime.GOOS}
}
