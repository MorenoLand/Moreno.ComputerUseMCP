//go:build windows

package platform

import (
	"context"
	"errors"
	"fmt"
	"time"
	"unsafe"

	"github.com/MorenoLand/Moreno.ComputerUseMCP/internal/computer"
	"golang.org/x/sys/windows"
)

const (
	swShowNoActivate  = 4
	swShowMinNoActive = 7

	wpfRestoreToMaximized = 0x0002

	dwmwaTransitionsForceDisabled = 3
	dwmwaCloaked                  = 14

	monitorDefaultToNearest = 2

	// minimizedSettleDelay gives a freshly restored window time to repaint
	// before it is captured.
	minimizedSettleDelay = 250 * time.Millisecond
)

var (
	dwmapi = windows.NewLazySystemDLL("dwmapi.dll")

	procGetWindowPlacement    = user32.NewProc("GetWindowPlacement")
	procSetWindowPlacement    = user32.NewProc("SetWindowPlacement")
	procMonitorFromRect       = user32.NewProc("MonitorFromRect")
	procGetMonitorInfo        = user32.NewProc("GetMonitorInfoW")
	procDwmSetWindowAttribute = dwmapi.NewProc("DwmSetWindowAttribute")
	procDwmGetWindowAttribute = dwmapi.NewProc("DwmGetWindowAttribute")
)

type nativeWindowPlacement struct {
	Length         uint32
	Flags          uint32
	ShowCmd        uint32
	MinPosition    nativePoint
	MaxPosition    nativePoint
	NormalPosition nativeRect
}

type nativeMonitorInfo struct {
	Size    uint32
	Monitor nativeRect
	Work    nativeRect
	Flags   uint32
}

func isMinimized(hwnd uintptr) bool {
	value, _, _ := procIsIconic.Call(hwnd)
	return value != 0
}

func isCloaked(hwnd uintptr) bool {
	var cloaked uint32
	hr, _, _ := procDwmGetWindowAttribute.Call(hwnd, dwmwaCloaked, uintptr(unsafe.Pointer(&cloaked)), unsafe.Sizeof(cloaked))
	return hr == 0 && cloaked != 0
}

func getWindowPlacement(hwnd uintptr) (nativeWindowPlacement, bool) {
	placement := nativeWindowPlacement{Length: uint32(unsafe.Sizeof(nativeWindowPlacement{}))}
	value, _, _ := procGetWindowPlacement.Call(hwnd, uintptr(unsafe.Pointer(&placement)))
	return placement, value != 0
}

func setWindowPlacement(hwnd uintptr, placement *nativeWindowPlacement) error {
	placement.Length = uint32(unsafe.Sizeof(*placement))
	if value, _, err := procSetWindowPlacement.Call(hwnd, uintptr(unsafe.Pointer(placement))); value == 0 {
		return win32Error("SetWindowPlacement", err)
	}
	return nil
}

func setWindowTransitions(hwnd uintptr, disabled bool) {
	var value uint32
	if disabled {
		value = 1
	}
	procDwmSetWindowAttribute.Call(hwnd, dwmwaTransitionsForceDisabled, uintptr(unsafe.Pointer(&value)), unsafe.Sizeof(value))
}

// restoredSize returns the size the window has when it is restored, which is
// the work area of its monitor when it was maximized before being minimized.
func restoredSize(placement nativeWindowPlacement) (int32, int32) {
	normal := placement.NormalPosition
	if placement.Flags&wpfRestoreToMaximized == 0 {
		return normal.Right - normal.Left, normal.Bottom - normal.Top
	}
	monitor, _, _ := procMonitorFromRect.Call(uintptr(unsafe.Pointer(&normal)), monitorDefaultToNearest)
	info := nativeMonitorInfo{Size: uint32(unsafe.Sizeof(nativeMonitorInfo{}))}
	if monitor != 0 {
		if value, _, _ := procGetMonitorInfo.Call(monitor, uintptr(unsafe.Pointer(&info))); value != 0 {
			return info.Work.Right - info.Work.Left, info.Work.Bottom - info.Work.Top
		}
	}
	return normal.Right - normal.Left, normal.Bottom - normal.Top
}

// captureMinimizedWindow renders a minimized window without disturbing the
// user: it is restored without activation at a position outside every monitor,
// captured, and then minimized again with its original placement. The screen
// origin reported is where the window would appear if restored.
func captureMinimizedWindow(ctx context.Context, hwnd uintptr, options computer.ScreenshotOptions) ([]byte, string, int, int, captureRect, error) {
	placement, ok := getWindowPlacement(hwnd)
	if !ok {
		return nil, "", 0, 0, captureRect{}, errors.New("minimized window has no placement")
	}
	width, height := restoredSize(placement)
	if width <= 0 || height <= 0 {
		return nil, "", 0, 0, captureRect{}, errors.New("minimized window has no restorable area")
	}
	origin := captureRect{
		Left:   int(placement.NormalPosition.Left),
		Top:    int(placement.NormalPosition.Top),
		Right:  int(placement.NormalPosition.Left + width),
		Bottom: int(placement.NormalPosition.Top + height),
	}

	offscreen := placement
	offscreen.Flags &^= wpfRestoreToMaximized
	offscreen.ShowCmd = swShowNoActivate
	left := int32(systemMetric(smXVirtualScreen)) - width - 128
	top := int32(systemMetric(smYVirtualScreen))
	offscreen.NormalPosition = nativeRect{Left: left, Top: top, Right: left + width, Bottom: top + height}

	setWindowTransitions(hwnd, true)
	defer setWindowTransitions(hwnd, false)
	if err := setWindowPlacement(hwnd, &offscreen); err != nil {
		return nil, "", 0, 0, captureRect{}, err
	}
	defer func() {
		restore := placement
		restore.ShowCmd = swShowMinNoActive
		_ = setWindowPlacement(hwnd, &restore)
	}()
	if isMinimized(hwnd) {
		return nil, "", 0, 0, captureRect{}, errors.New("window refused to restore for capture")
	}
	select {
	case <-ctx.Done():
		return nil, "", 0, 0, captureRect{}, ctx.Err()
	case <-time.After(minimizedSettleDelay):
	}
	rect, ok := windowRect(hwnd)
	if !ok {
		return nil, "", 0, 0, captureRect{}, errors.New("restored window has no captureable bounds")
	}
	area, _, err := validCaptureRect(rect, "")
	if err != nil {
		return nil, "", 0, 0, captureRect{}, fmt.Errorf("restored window: %w", err)
	}
	// The pointer is not over the off-screen copy, so never draw it.
	options.IncludeCursor = false
	data, mimeType, w, h, err := captureWindow(ctx, hwnd, area, options)
	return data, mimeType, w, h, origin, err
}
