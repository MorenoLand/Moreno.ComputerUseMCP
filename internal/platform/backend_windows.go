//go:build windows

package platform

import (
	"context"
	"errors"
	"fmt"
	"image"
	"strconv"
	"strings"
	"syscall"
	"unicode/utf16"
	"unsafe"

	"github.com/MorenoLand/Moreno.ComputerUseMCP/internal/computer"
	"golang.org/x/sys/windows"
)

const (
	smXVirtualScreen  = 76
	smYVirtualScreen  = 77
	smCXVirtualScreen = 78
	smCYVirtualScreen = 79

	srccopy      = 0x00CC0020
	captureblt   = 0x40000000
	biRGB        = 0
	dibRGBColors = 0
	diNormal     = 0x0003

	inputMouse    = 0
	inputKeyboard = 1

	mouseEventLeftDown   = 0x0002
	mouseEventLeftUp     = 0x0004
	mouseEventRightDown  = 0x0008
	mouseEventRightUp    = 0x0010
	mouseEventMiddleDown = 0x0020
	mouseEventMiddleUp   = 0x0040
	mouseEventXDown      = 0x0080
	mouseEventXUp        = 0x0100

	keyEventKeyUp   = 0x0002
	keyEventUnicode = 0x0004
)

var (
	user32 = windows.NewLazySystemDLL("user32.dll")
	gdi32  = windows.NewLazySystemDLL("gdi32.dll")

	procGetDC                         = user32.NewProc("GetDC")
	procReleaseDC                     = user32.NewProc("ReleaseDC")
	procGetSystemMetrics              = user32.NewProc("GetSystemMetrics")
	procGetForegroundWindow           = user32.NewProc("GetForegroundWindow")
	procGetWindowRect                 = user32.NewProc("GetWindowRect")
	procIsWindow                      = user32.NewProc("IsWindow")
	procIsWindowVisible               = user32.NewProc("IsWindowVisible")
	procIsIconic                      = user32.NewProc("IsIconic")
	procGetWindowTextLength           = user32.NewProc("GetWindowTextLengthW")
	procGetWindowText                 = user32.NewProc("GetWindowTextW")
	procEnumWindows                   = user32.NewProc("EnumWindows")
	procGetCursorInfo                 = user32.NewProc("GetCursorInfo")
	procDrawIconEx                    = user32.NewProc("DrawIconEx")
	procSetCursorPos                  = user32.NewProc("SetCursorPos")
	procSendInput                     = user32.NewProc("SendInput")
	procSetProcessDPIAware            = user32.NewProc("SetProcessDPIAware")
	procSetProcessDPIAwarenessContext = user32.NewProc("SetProcessDpiAwarenessContext")

	procCreateCompatibleDC     = gdi32.NewProc("CreateCompatibleDC")
	procDeleteDC               = gdi32.NewProc("DeleteDC")
	procCreateCompatibleBitmap = gdi32.NewProc("CreateCompatibleBitmap")
	procSelectObject           = gdi32.NewProc("SelectObject")
	procDeleteObject           = gdi32.NewProc("DeleteObject")
	procBitBlt                 = gdi32.NewProc("BitBlt")
	procGetDIBits              = gdi32.NewProc("GetDIBits")
)

type nativePoint struct {
	X int32
	Y int32
}

type nativeRect struct {
	Left   int32
	Top    int32
	Right  int32
	Bottom int32
}

type bitmapInfoHeader struct {
	Size          uint32
	Width         int32
	Height        int32
	Planes        uint16
	BitCount      uint16
	Compression   uint32
	SizeImage     uint32
	XPelsPerMeter int32
	YPelsPerMeter int32
	ClrUsed       uint32
	ClrImportant  uint32
}

type bitmapInfo struct {
	Header bitmapInfoHeader
	Colors [1]uint32
}

type nativeCursorInfo struct {
	Size     uint32
	Flags    uint32
	Cursor   uintptr
	Position nativePoint
}

type nativeMouseInput struct {
	DX        int32
	DY        int32
	MouseData uint32
	Flags     uint32
	Time      uint32
	ExtraInfo uintptr
}

type nativeKeyboardInput struct {
	VirtualKey uint16
	ScanCode   uint16
	Flags      uint32
	Time       uint32
	ExtraInfo  uintptr
}

type nativeInput struct {
	Type    uint32
	Padding uint32
	Data    [32]byte
}

type captureRect struct {
	Left   int
	Top    int
	Right  int
	Bottom int
}

type windowsBackend struct{}

func newBackend() computer.Backend {
	setDPIAwareness()
	return &windowsBackend{}
}

func (b *windowsBackend) Screenshot(ctx context.Context, options computer.ScreenshotOptions) (computer.Screenshot, error) {
	rect, windowID, err := b.targetRect(options)
	if err != nil {
		return computer.Screenshot{}, err
	}
	data, mimeType, width, height, err := capture(ctx, rect, options)
	if err != nil {
		return computer.Screenshot{}, err
	}
	return computer.Screenshot{
		Data:        data,
		MIMEType:    mimeType,
		Width:       width,
		Height:      height,
		OriginX:     rect.Left,
		OriginY:     rect.Top,
		ScaleFactor: 1,
		Target:      options.Target,
		WindowID:    windowID,
	}, nil
}

func (b *windowsBackend) ListWindows(ctx context.Context) ([]computer.Window, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var result []computer.Window
	callback := windows.NewCallback(func(hwnd uintptr, _ uintptr) uintptr {
		visible, _, _ := procIsWindowVisible.Call(hwnd)
		if visible == 0 {
			return 1
		}
		title := windowTitle(hwnd)
		if title == "" {
			return 1
		}
		rect, ok := windowRect(hwnd)
		if !ok || rect.Right <= rect.Left || rect.Bottom <= rect.Top {
			return 1
		}
		result = append(result, computer.Window{
			ID:      formatWindowID(hwnd),
			Title:   title,
			X:       int(rect.Left),
			Y:       int(rect.Top),
			Width:   int(rect.Right - rect.Left),
			Height:  int(rect.Bottom - rect.Top),
			Visible: true,
			Minimized: func() bool {
				minimized, _, _ := procIsIconic.Call(hwnd)
				return minimized != 0
			}(),
		})
		return 1
	})
	if value, _, err := procEnumWindows.Call(callback, 0); value == 0 {
		return nil, win32Error("EnumWindows", err)
	}
	return result, nil
}

func (b *windowsBackend) Move(ctx context.Context, point computer.Point) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if value, _, err := procSetCursorPos.Call(uintptr(int32(point.X)), uintptr(int32(point.Y))); value == 0 {
		return win32Error("SetCursorPos", err)
	}
	return nil
}

func (b *windowsBackend) Click(ctx context.Context, options computer.ClickOptions) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if value, _, err := procSetCursorPos.Call(uintptr(int32(options.Point.X)), uintptr(int32(options.Point.Y))); value == 0 {
		return win32Error("SetCursorPos", err)
	}
	inputs := make([]nativeInput, 0, len(options.Modifiers)*2+options.Clicks*2)
	for _, modifier := range options.Modifiers {
		vk, err := virtualKey(modifier)
		if err != nil {
			return err
		}
		inputs = append(inputs, keyboardInput(vk, 0))
	}
	down, up, mouseData, err := mouseButton(options.Button)
	if err != nil {
		return err
	}
	for range options.Clicks {
		inputs = append(inputs, mouseInput(mouseData, down), mouseInput(mouseData, up))
	}
	for index := len(options.Modifiers) - 1; index >= 0; index-- {
		vk, err := virtualKey(options.Modifiers[index])
		if err != nil {
			return err
		}
		inputs = append(inputs, keyboardInput(vk, keyEventKeyUp))
	}
	return sendInputs(inputs)
}

func (b *windowsBackend) Type(ctx context.Context, options computer.TypeOptions) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	units := utf16.Encode([]rune(options.Text))
	inputs := make([]nativeInput, 0, len(units)*2)
	for _, unit := range units {
		inputs = append(inputs, keyboardInput(unit, keyEventUnicode), keyboardInput(unit, keyEventUnicode|keyEventKeyUp))
	}
	return sendInputs(inputs)
}

func (b *windowsBackend) Keypress(ctx context.Context, options computer.KeypressOptions) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	keys := expandKeys(options.Keys)
	inputs := make([]nativeInput, 0, len(keys)*2)
	for _, key := range keys {
		vk, err := virtualKey(key)
		if err != nil {
			return err
		}
		inputs = append(inputs, keyboardInput(vk, 0))
	}
	for index := len(keys) - 1; index >= 0; index-- {
		vk, err := virtualKey(keys[index])
		if err != nil {
			return err
		}
		inputs = append(inputs, keyboardInput(vk, keyEventKeyUp))
	}
	return sendInputs(inputs)
}

func (b *windowsBackend) Close() error {
	return nil
}

func (b *windowsBackend) targetRect(options computer.ScreenshotOptions) (captureRect, string, error) {
	switch options.Target {
	case computer.TargetScreen:
		left := systemMetric(smXVirtualScreen)
		top := systemMetric(smYVirtualScreen)
		width := systemMetric(smCXVirtualScreen)
		height := systemMetric(smCYVirtualScreen)
		if width <= 0 || height <= 0 {
			return captureRect{}, "", errors.New("virtual desktop has no captureable area")
		}
		return captureRect{Left: left, Top: top, Right: left + width, Bottom: top + height}, "", nil
	case computer.TargetActiveWindow:
		hwnd, _, _ := procGetForegroundWindow.Call()
		if hwnd == 0 {
			return captureRect{}, "", errors.New("no foreground window is available")
		}
		rect, ok := windowRect(hwnd)
		if !ok {
			return captureRect{}, "", errors.New("foreground window has no captureable bounds")
		}
		return validCaptureRect(rect, formatWindowID(hwnd))
	case computer.TargetWindow:
		hwnd, err := parseWindowID(options.WindowID)
		if err != nil {
			return captureRect{}, "", err
		}
		rect, ok := windowRect(hwnd)
		if !ok {
			return captureRect{}, "", errors.New("window has no captureable bounds")
		}
		return validCaptureRect(rect, formatWindowID(hwnd))
	default:
		return captureRect{}, "", fmt.Errorf("unsupported screenshot target %q", options.Target)
	}
}

func capture(ctx context.Context, rect captureRect, options computer.ScreenshotOptions) ([]byte, string, int, int, error) {
	if err := ctx.Err(); err != nil {
		return nil, "", 0, 0, err
	}
	width := rect.Right - rect.Left
	height := rect.Bottom - rect.Top
	if width <= 0 || height <= 0 {
		return nil, "", 0, 0, errors.New("capture rectangle has no area")
	}
	desktopDC, _, desktopErr := procGetDC.Call(0)
	if desktopDC == 0 {
		return nil, "", 0, 0, win32Error("GetDC", desktopErr)
	}
	defer procReleaseDC.Call(0, desktopDC)
	memoryDC, _, memoryErr := procCreateCompatibleDC.Call(desktopDC)
	if memoryDC == 0 {
		return nil, "", 0, 0, win32Error("CreateCompatibleDC", memoryErr)
	}
	defer procDeleteDC.Call(memoryDC)
	bitmap, _, bitmapErr := procCreateCompatibleBitmap.Call(desktopDC, uintptr(width), uintptr(height))
	if bitmap == 0 {
		return nil, "", 0, 0, win32Error("CreateCompatibleBitmap", bitmapErr)
	}
	defer procDeleteObject.Call(bitmap)
	previous, _, selectErr := procSelectObject.Call(memoryDC, bitmap)
	if previous == 0 || previous == ^uintptr(0) {
		return nil, "", 0, 0, win32Error("SelectObject", selectErr)
	}
	defer procSelectObject.Call(memoryDC, previous)
	rop := uintptr(uint32(srccopy | captureblt))
	if value, _, err := procBitBlt.Call(memoryDC, 0, 0, uintptr(width), uintptr(height), desktopDC, uintptr(int32(rect.Left)), uintptr(int32(rect.Top)), rop); value == 0 {
		return nil, "", 0, 0, win32Error("BitBlt", err)
	}
	if options.IncludeCursor {
		drawCursor(memoryDC, rect)
	}
	pixels := make([]byte, width*height*4)
	info := bitmapInfo{Header: bitmapInfoHeader{
		Size:        uint32(unsafe.Sizeof(bitmapInfoHeader{})),
		Width:       int32(width),
		Height:      -int32(height),
		Planes:      1,
		BitCount:    32,
		Compression: biRGB,
	}}
	if value, _, err := procGetDIBits.Call(memoryDC, bitmap, 0, uintptr(height), uintptr(unsafe.Pointer(&pixels[0])), uintptr(unsafe.Pointer(&info)), dibRGBColors); value == 0 {
		return nil, "", 0, 0, win32Error("GetDIBits", err)
	}
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			source := (y*width + x) * 4
			target := y*img.Stride + x*4
			img.Pix[target] = pixels[source+2]
			img.Pix[target+1] = pixels[source+1]
			img.Pix[target+2] = pixels[source]
			img.Pix[target+3] = 0xff
		}
	}
	return computer.EncodeImage(img, options)
}

func drawCursor(hdc uintptr, rect captureRect) {
	info := nativeCursorInfo{Size: uint32(unsafe.Sizeof(nativeCursorInfo{}))}
	if value, _, _ := procGetCursorInfo.Call(uintptr(unsafe.Pointer(&info))); value == 0 || info.Flags == 0 || info.Cursor == 0 {
		return
	}
	x := int(info.Position.X) - rect.Left
	y := int(info.Position.Y) - rect.Top
	procDrawIconEx.Call(hdc, uintptr(int32(x)), uintptr(int32(y)), info.Cursor, 0, 0, 0, 0, diNormal)
}

func systemMetric(index int) int {
	value, _, _ := procGetSystemMetrics.Call(uintptr(index))
	return int(int32(value))
}

func windowRect(hwnd uintptr) (nativeRect, bool) {
	var rect nativeRect
	value, _, _ := procGetWindowRect.Call(hwnd, uintptr(unsafe.Pointer(&rect)))
	return rect, value != 0
}

func windowTitle(hwnd uintptr) string {
	length, _, _ := procGetWindowTextLength.Call(hwnd)
	if length == 0 {
		return ""
	}
	buffer := make([]uint16, int(length)+1)
	procGetWindowText.Call(hwnd, uintptr(unsafe.Pointer(&buffer[0])), uintptr(len(buffer)))
	return windows.UTF16ToString(buffer)
}

func parseWindowID(value string) (uintptr, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0, errors.New("window_id is empty")
	}
	hwnd, err := strconv.ParseUint(value, 0, 64)
	if err != nil || hwnd == 0 {
		return 0, fmt.Errorf("invalid window_id %q", value)
	}
	if isWindow, _, _ := procIsWindow.Call(uintptr(hwnd)); isWindow == 0 {
		return 0, fmt.Errorf("window_id %q is not a live window", value)
	}
	return uintptr(hwnd), nil
}

func formatWindowID(hwnd uintptr) string {
	return fmt.Sprintf("0x%X", hwnd)
}

func validCaptureRect(rect nativeRect, windowID string) (captureRect, string, error) {
	if rect.Right <= rect.Left || rect.Bottom <= rect.Top {
		return captureRect{}, "", errors.New("window has no captureable area")
	}
	return captureRect{Left: int(rect.Left), Top: int(rect.Top), Right: int(rect.Right), Bottom: int(rect.Bottom)}, windowID, nil
}

func setDPIAwareness() {
	const perMonitorAwareV2 = ^uintptr(3)
	if value, _, _ := procSetProcessDPIAwarenessContext.Call(perMonitorAwareV2); value == 0 {
		procSetProcessDPIAware.Call()
	}
}

func mouseButton(button string) (uint32, uint32, uint32, error) {
	switch button {
	case "left":
		return mouseEventLeftDown, mouseEventLeftUp, 0, nil
	case "right":
		return mouseEventRightDown, mouseEventRightUp, 0, nil
	case "middle":
		return mouseEventMiddleDown, mouseEventMiddleUp, 0, nil
	case "x1":
		return mouseEventXDown, mouseEventXUp, 1, nil
	case "x2":
		return mouseEventXDown, mouseEventXUp, 2, nil
	default:
		return 0, 0, 0, fmt.Errorf("invalid mouse button %q", button)
	}
}

func sendInputs(inputs []nativeInput) error {
	if len(inputs) == 0 {
		return nil
	}
	value, _, err := procSendInput.Call(uintptr(len(inputs)), uintptr(unsafe.Pointer(&inputs[0])), unsafe.Sizeof(nativeInput{}))
	if value != uintptr(len(inputs)) {
		return win32Error("SendInput", err)
	}
	return nil
}

func mouseInput(mouseData uint32, flags uint32) nativeInput {
	result := nativeInput{Type: inputMouse}
	*(*nativeMouseInput)(unsafe.Pointer(&result.Data[0])) = nativeMouseInput{MouseData: mouseData, Flags: flags}
	return result
}

func keyboardInput(vk uint16, flags uint32) nativeInput {
	result := nativeInput{Type: inputKeyboard}
	*(*nativeKeyboardInput)(unsafe.Pointer(&result.Data[0])) = nativeKeyboardInput{VirtualKey: vk, Flags: flags}
	return result
}

func expandKeys(keys []string) []string {
	result := make([]string, 0, len(keys))
	for _, key := range keys {
		for _, part := range strings.Split(key, "+") {
			if value := strings.TrimSpace(part); value != "" {
				result = append(result, value)
			}
		}
	}
	return result
}

func virtualKey(value string) (uint16, error) {
	key := strings.ToUpper(strings.TrimSpace(value))
	switch key {
	case "BACKSPACE", "BACK":
		return 0x08, nil
	case "TAB":
		return 0x09, nil
	case "ENTER", "RETURN":
		return 0x0D, nil
	case "SHIFT":
		return 0x10, nil
	case "CTRL", "CONTROL":
		return 0x11, nil
	case "ALT", "OPTION":
		return 0x12, nil
	case "PAUSE":
		return 0x13, nil
	case "CAPSLOCK", "CAPS_LOCK":
		return 0x14, nil
	case "ESC", "ESCAPE":
		return 0x1B, nil
	case "SPACE":
		return 0x20, nil
	case "PAGEUP", "PAGE_UP":
		return 0x21, nil
	case "PAGEDOWN", "PAGE_DOWN":
		return 0x22, nil
	case "END":
		return 0x23, nil
	case "HOME":
		return 0x24, nil
	case "LEFT", "ARROWLEFT", "ARROW_LEFT":
		return 0x25, nil
	case "UP", "ARROWUP", "ARROW_UP":
		return 0x26, nil
	case "RIGHT", "ARROWRIGHT", "ARROW_RIGHT":
		return 0x27, nil
	case "DOWN", "ARROWDOWN", "ARROW_DOWN":
		return 0x28, nil
	case "INSERT", "INS":
		return 0x2D, nil
	case "DELETE", "DEL":
		return 0x2E, nil
	case "WIN", "WINDOWS", "META", "SUPER", "COMMAND", "CMD":
		return 0x5B, nil
	case "APPS", "MENU", "CONTEXT_MENU":
		return 0x5D, nil
	case "NUMLOCK", "NUM_LOCK":
		return 0x90, nil
	case "PRINTSCREEN", "PRINT_SCREEN":
		return 0x2C, nil
	case "SCROLLLOCK", "SCROLL_LOCK":
		return 0x91, nil
	case "GRAVE":
		return 0xC0, nil
	}
	if len(key) >= 2 && key[0] == 'F' {
		number, err := strconv.Atoi(key[1:])
		if err == nil && number >= 1 && number <= 24 {
			return uint16(0x6F + number), nil
		}
	}
	if len([]rune(key)) == 1 {
		character := []rune(key)[0]
		switch {
		case character >= 'A' && character <= 'Z':
			return uint16(character), nil
		case character >= '0' && character <= '9':
			return uint16(character), nil
		case character == '-':
			return 0xBD, nil
		case character == '=':
			return 0xBB, nil
		case character == '[':
			return 0xDB, nil
		case character == ']':
			return 0xDD, nil
		case character == '\\':
			return 0xDC, nil
		case character == ';':
			return 0xBA, nil
		case character == '\'':
			return 0xDE, nil
		case character == ',':
			return 0xBC, nil
		case character == '.':
			return 0xBE, nil
		case character == '/':
			return 0xBF, nil
		case character == 96:
			return 0xC0, nil
		}
	}
	return 0, fmt.Errorf("unsupported key %q", value)
}

func win32Error(operation string, err error) error {
	if err == nil || err == syscall.Errno(0) {
		err = windows.GetLastError()
	}
	return fmt.Errorf("%s failed: %w", operation, err)
}

var _ computer.Backend = (*windowsBackend)(nil)
