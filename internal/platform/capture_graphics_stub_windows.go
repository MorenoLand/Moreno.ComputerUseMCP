//go:build windows && !amd64

package platform

import (
	"context"
	"errors"

	"github.com/MorenoLand/Moreno.ComputerUseMCP/internal/computer"
)

func captureWindowGraphics(context.Context, uintptr, computer.ScreenshotOptions) ([]byte, string, int, int, error) {
	return nil, "", 0, 0, errors.New("Windows Graphics Capture requires amd64")
}
