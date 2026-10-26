//go:build windows

package activity

import (
	"log"
	"runtime"
	"sync"
	"sync/atomic"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	wmClose                    = 0x0010
	wmDestroy                  = 0x0002
	wmPaint                    = 0x000F
	wmEraseBackground          = 0x0014
	wmNCHitTest                = 0x0084
	wmMouseActivate            = 0x0021
	wmActivityUpdate           = 0x8001
	vkEscape                   = 0x1B
	activityStatusWidth        = 360
	activityStatusHeight       = 56
	activityStatusMargin       = 24
	activityExcludeFromCapture = 0x11
	maNoActivate               = 3
	htTransparent              = ^uintptr(0)
	wsPopup                    = 0x80000000
	wsExToolWindow             = 0x00000080
	wsExTopmost                = 0x00000008
	wsExTransparent            = 0x00000020
	wsExNoActivate             = 0x08000000
	swpNoActivate              = 0x00000010
	swpNoMove                  = 0x00000002
	swpNoSize                  = 0x00000001
	swHide                     = 0
	swShowNoActivate           = 4
	dtNoPrefix                 = 0x00000800
	transparent                = 1
	defaultGUIFont             = 17
)

var (
	activityUser32                 = windows.NewLazySystemDLL("user32.dll")
	activityGDI32                  = windows.NewLazySystemDLL("gdi32.dll")
	procActivityGetModuleHandle    = activityKernel32.NewProc("GetModuleHandleW")
	procActivityRegisterClassEx    = activityUser32.NewProc("RegisterClassExW")
	procActivityCreateWindowEx     = activityUser32.NewProc("CreateWindowExW")
	procActivityDefWindowProc      = activityUser32.NewProc("DefWindowProcW")
	procActivityGetDC              = activityUser32.NewProc("GetDC")
	procActivityReleaseDC          = activityUser32.NewProc("ReleaseDC")
	procActivityDestroyWindow      = activityUser32.NewProc("DestroyWindow")
	procActivityPostQuitMessage    = activityUser32.NewProc("PostQuitMessage")
	procActivityPostMessage        = activityUser32.NewProc("PostMessageW")
	procActivityGetMessage         = activityUser32.NewProc("GetMessageW")
	procActivityTranslateMessage   = activityUser32.NewProc("TranslateMessage")
	procActivityDispatchMessage    = activityUser32.NewProc("DispatchMessageW")
	procActivityShowWindow         = activityUser32.NewProc("ShowWindow")
	procActivityUpdateWindow       = activityUser32.NewProc("UpdateWindow")
	procActivitySetWindowPos       = activityUser32.NewProc("SetWindowPos")
	procActivityInvalidateRect     = activityUser32.NewProc("InvalidateRect")
	procActivityGetAsyncKeyState   = activityUser32.NewProc("GetAsyncKeyState")
	procActivityGetSystemMetrics   = activityUser32.NewProc("GetSystemMetrics")
	procActivityBeginPaint         = activityUser32.NewProc("BeginPaint")
	procActivityEndPaint           = activityUser32.NewProc("EndPaint")
	procActivityGetClientRect      = activityUser32.NewProc("GetClientRect")
	procActivitySetDisplayAffinity = activityUser32.NewProc("SetWindowDisplayAffinity")
	procActivityDrawText           = activityUser32.NewProc("DrawTextW")
	procActivitySetBkMode          = activityGDI32.NewProc("SetBkMode")
	procActivitySetTextColor       = activityGDI32.NewProc("SetTextColor")
	procActivityGetStockObject     = activityGDI32.NewProc("GetStockObject")
	procActivitySelectObject       = activityGDI32.NewProc("SelectObject")
	procActivityCreateSolidBrush   = activityGDI32.NewProc("CreateSolidBrush")
	procActivityFillRect           = activityUser32.NewProc("FillRect")
	procActivityDeleteObject       = activityGDI32.NewProc("DeleteObject")
)

var activityKernel32 = windows.NewLazySystemDLL("kernel32.dll")

type controller struct {
	abort      func()
	mu         sync.RWMutex
	client     string
	active     bool
	hwnd       uintptr
	border     [4]uintptr
	ready      chan error
	done       chan struct{}
	hotkeyStop chan struct{}
	hotkeyDone chan struct{}
	closeOnce  sync.Once
}

type activityWndClass struct {
	Size       uint32
	Style      uint32
	WndProc    uintptr
	ClsExtra   int32
	WndExtra   int32
	Instance   uintptr
	Icon       uintptr
	Cursor     uintptr
	Background uintptr
	Menu       *uint16
	ClassName  *uint16
	IconSmall  uintptr
}

type activityMessage struct {
	Window  uintptr
	Message uint32
	Padding uint32
	WParam  uintptr
	LParam  uintptr
	Time    uint32
	Point   nativeActivityPoint
	Private uint32
}

type nativeActivityPoint struct {
	X int32
	Y int32
}

type activityPaintStruct struct {
	DC        uintptr
	Erase     uint32
	PaintRect nativeActivityRect
	Restore   uint32
	IncUpdate uint32
	Reserved  [32]byte
}

type nativeActivityRect struct {
	Left   int32
	Top    int32
	Right  int32
	Bottom int32
}

var currentWindowsController atomic.Pointer[controller]
var activityWindowProc = windows.NewCallback(func(hwnd, message, wParam, lParam uintptr) uintptr {
	controller := currentWindowsController.Load()
	switch uint32(message) {
	case wmActivityUpdate:
		if controller != nil {
			controller.update(hwnd)
		}
		return 0
	case wmNCHitTest:
		return htTransparent
	case wmMouseActivate:
		return maNoActivate
	case wmEraseBackground:
		return 1
	case wmPaint:
		if controller != nil {
			controller.paint(hwnd)
			return 0
		}
	case wmClose:
		if controller != nil {
			controller.destroyWindows()
		} else {
			procActivityDestroyWindow.Call(hwnd)
		}
		return 0
	case wmDestroy:
		if controller != nil && hwnd == controller.hwnd {
			procActivityPostQuitMessage.Call(0)
		}
		return 0
	}
	value, _, _ := procActivityDefWindowProc.Call(hwnd, message, wParam, lParam)
	if message == wmClose {
		return value
	}
	return value
})

func newController(abort func()) Controller {
	controller := &controller{abort: abort, client: "AI", ready: make(chan error, 1), done: make(chan struct{})}
	currentWindowsController.Store(controller)
	go controller.run()
	if err := <-controller.ready; err != nil {
		log.Printf("computer-use activity indicator disabled: %v", err)
		currentWindowsController.CompareAndSwap(controller, nil)
	}
	return controller
}

func (c *controller) run() {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	if err := c.setup(); err != nil {
		c.ready <- err
		close(c.done)
		return
	}
	c.ready <- nil
	c.hotkeyStop = make(chan struct{})
	c.hotkeyDone = make(chan struct{})
	go c.pollEscape()
	var message activityMessage
	for {
		value, _, _ := procActivityGetMessage.Call(uintptr(unsafe.Pointer(&message)), 0, 0, 0)
		if value == 0 || value == ^uintptr(0) {
			break
		}
		procActivityTranslateMessage.Call(uintptr(unsafe.Pointer(&message)))
		procActivityDispatchMessage.Call(uintptr(unsafe.Pointer(&message)))
	}
	close(c.hotkeyStop)
	<-c.hotkeyDone
	if c.hwnd != 0 {
		c.destroyWindows()
	}
	currentWindowsController.CompareAndSwap(c, nil)
	close(c.done)
}

func (c *controller) setup() error {
	instance, _, err := procActivityGetModuleHandle.Call(0)
	if instance == 0 {
		return win32ActivityError("GetModuleHandle", err)
	}
	name := windows.StringToUTF16Ptr("ComputerUseActivityIndicator")
	class := activityWndClass{Size: uint32(unsafe.Sizeof(activityWndClass{})), WndProc: activityWindowProc, Instance: instance, ClassName: name}
	if value, _, err := procActivityRegisterClassEx.Call(uintptr(unsafe.Pointer(&class))); value == 0 && windows.GetLastError() != windows.ERROR_CLASS_ALREADY_EXISTS {
		return win32ActivityError("RegisterClassEx", err)
	}
	width, height := activityStatusWidth, activityStatusHeight
	xVirtual := activitySystemMetric(76)
	yVirtual := activitySystemMetric(77)
	widthVirtual := activitySystemMetric(78)
	heightVirtual := activitySystemMetric(79)
	x := xVirtual + (widthVirtual-width)/2
	y := yVirtual + heightVirtual - height - activityStatusMargin
	hwnd, _, err := procActivityCreateWindowEx.Call(wsExToolWindow|wsExTopmost|wsExTransparent|wsExNoActivate, uintptr(unsafe.Pointer(name)), uintptr(unsafe.Pointer(name)), wsPopup, uintptr(int32(x)), uintptr(int32(y)), uintptr(width), uintptr(height), 0, 0, instance, 0)
	if hwnd == 0 {
		return win32ActivityError("CreateWindowEx", err)
	}
	c.hwnd = hwnd
	protectActivityWindow(hwnd)
	procActivitySetWindowPos.Call(hwnd, ^uintptr(0), uintptr(int32(x)), uintptr(int32(y)), uintptr(width), uintptr(height), swpNoActivate)
	borderThickness := 2
	borderWindows := [][4]int{
		{xVirtual, yVirtual, widthVirtual, borderThickness},
		{xVirtual, yVirtual + heightVirtual - borderThickness, widthVirtual, borderThickness},
		{xVirtual, yVirtual + borderThickness, borderThickness, heightVirtual - borderThickness*2},
		{xVirtual + widthVirtual - borderThickness, yVirtual + borderThickness, borderThickness, heightVirtual - borderThickness*2},
	}
	for index, border := range borderWindows {
		borderHwnd, _, borderErr := procActivityCreateWindowEx.Call(wsExToolWindow|wsExTopmost|wsExTransparent|wsExNoActivate, uintptr(unsafe.Pointer(name)), uintptr(unsafe.Pointer(name)), wsPopup, uintptr(int32(border[0])), uintptr(int32(border[1])), uintptr(int32(border[2])), uintptr(int32(border[3])), 0, 0, instance, 0)
		if borderHwnd == 0 {
			c.destroyWindows()
			return win32ActivityError("CreateWindowEx border", borderErr)
		}
		c.border[index] = borderHwnd
		protectActivityWindow(borderHwnd)
	}
	procActivityInvalidateRect.Call(hwnd, 0, 1)
	procActivityUpdateWindow.Call(hwnd)
	c.update(hwnd)
	return nil
}

func (c *controller) pollEscape() {
	defer close(c.hotkeyDone)
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	wasDown := false
	for {
		select {
		case <-c.hotkeyStop:
			return
		case <-ticker.C:
			state, _, _ := procActivityGetAsyncKeyState.Call(vkEscape)
			down := state&0x8000 != 0
			if down && !wasDown {
				c.abortNow()
			}
			wasDown = down
		}
	}
}

func (c *controller) Begin() {
	c.mu.Lock()
	c.active = true
	c.mu.Unlock()
	c.invalidate()
}

func (c *controller) End() {
	c.mu.Lock()
	c.active = false
	c.mu.Unlock()
	c.invalidate()
}

func (c *controller) SetClient(value string) {
	c.mu.Lock()
	c.client = clientLabel(value)
	c.mu.Unlock()
	c.invalidate()
}

func (c *controller) abortNow() {
	if c.abort != nil {
		c.abort()
	}
}

func (c *controller) invalidate() {
	if c.hwnd != 0 {
		procActivityPostMessage.Call(c.hwnd, wmActivityUpdate, 0, 0)
	}
}

func (c *controller) update(hwnd uintptr) {
	c.mu.RLock()
	active := c.active
	c.mu.RUnlock()
	if active {
		procActivitySetWindowPos.Call(hwnd, ^uintptr(0), 0, 0, 0, 0, swpNoActivate|swpNoMove|swpNoSize)
		procActivityShowWindow.Call(hwnd, swShowNoActivate)
		procActivityInvalidateRect.Call(hwnd, 0, 0)
		procActivityUpdateWindow.Call(hwnd)
		c.paintNow(hwnd)
	} else {
		procActivityShowWindow.Call(hwnd, swHide)
	}
	for _, border := range c.border {
		if border == 0 {
			continue
		}
		if active {
			procActivitySetWindowPos.Call(border, ^uintptr(0), 0, 0, 0, 0, swpNoActivate|swpNoMove|swpNoSize)
			procActivityShowWindow.Call(border, swShowNoActivate)
			procActivityInvalidateRect.Call(border, 0, 0)
			procActivityUpdateWindow.Call(border)
			c.paintNow(border)
		} else {
			procActivityShowWindow.Call(border, swHide)
		}
	}
	if active {
		procActivitySetWindowPos.Call(c.hwnd, ^uintptr(0), 0, 0, 0, 0, swpNoActivate|swpNoMove|swpNoSize)
	}
}

func (c *controller) isBorder(hwnd uintptr) bool {
	for _, border := range c.border {
		if hwnd == border {
			return true
		}
	}
	return false
}

func (c *controller) paint(hwnd uintptr) {
	var paint activityPaintStruct
	dc, _, _ := procActivityBeginPaint.Call(hwnd, uintptr(unsafe.Pointer(&paint)))
	if dc == 0 {
		return
	}
	defer procActivityEndPaint.Call(hwnd, uintptr(unsafe.Pointer(&paint)))
	c.draw(hwnd, dc)
}

func (c *controller) paintNow(hwnd uintptr) {
	dc, _, _ := procActivityGetDC.Call(hwnd)
	if dc == 0 {
		return
	}
	c.draw(hwnd, dc)
	procActivityReleaseDC.Call(hwnd, dc)
}

func (c *controller) draw(hwnd, dc uintptr) {
	if c.isBorder(hwnd) {
		color := uint32(0x006B7F94)
		var rect nativeActivityRect
		if value, _, _ := procActivityGetClientRect.Call(hwnd, uintptr(unsafe.Pointer(&rect))); value != 0 {
			fillActivityRect(dc, rect, color)
		}
		return
	}
	c.mu.RLock()
	client := c.client
	active := c.active
	c.mu.RUnlock()
	background := uint32(0x00242424)
	stripe := uint32(0x003D9BEA)
	if active {
		stripe = 0x0000A5FF
	}
	fillActivityRect(dc, nativeActivityRect{Right: activityStatusWidth, Bottom: activityStatusHeight}, background)
	fillActivityRect(dc, nativeActivityRect{Right: activityStatusWidth, Bottom: 3}, stripe)
	font, _, _ := procActivityGetStockObject.Call(defaultGUIFont)
	previousFont, _, _ := procActivitySelectObject.Call(dc, font)
	defer procActivitySelectObject.Call(dc, previousFont)
	procActivitySetBkMode.Call(dc, transparent)
	procActivitySetTextColor.Call(dc, 0x00FFFFFF)
	drawActivityText(dc, "Computer controlled by "+client, nativeActivityRect{Left: 10, Top: 8, Right: activityStatusWidth - 10, Bottom: 30})
	procActivitySetTextColor.Call(dc, 0x00D0D0D0)
	drawActivityText(dc, "Esc to stop", nativeActivityRect{Left: 10, Top: 31, Right: activityStatusWidth - 10, Bottom: 52})
}

func fillActivityRect(dc uintptr, rect nativeActivityRect, color uint32) {
	brush, _, _ := procActivityCreateSolidBrush.Call(uintptr(color))
	if brush == 0 {
		return
	}
	procActivityFillRect.Call(dc, uintptr(unsafe.Pointer(&rect)), brush)
	procActivityDeleteObject.Call(brush)
}

func drawActivityText(dc uintptr, value string, rect nativeActivityRect) {
	text := windows.StringToUTF16Ptr(value)
	procActivityDrawText.Call(dc, uintptr(unsafe.Pointer(text)), uintptr(^uint32(0)), uintptr(unsafe.Pointer(&rect)), dtNoPrefix)
}

func protectActivityWindow(hwnd uintptr) {
	procActivitySetDisplayAffinity.Call(hwnd, activityExcludeFromCapture)
}

func (c *controller) destroyWindows() {
	for index, border := range c.border {
		if border != 0 {
			procActivityDestroyWindow.Call(border)
			c.border[index] = 0
		}
	}
	if c.hwnd != 0 {
		procActivityDestroyWindow.Call(c.hwnd)
		c.hwnd = 0
	}
}

func activitySystemMetric(index uintptr) int {
	value, _, _ := procActivityGetSystemMetrics.Call(index)
	return int(int32(value))
}

func win32ActivityError(operation string, err error) error {
	if err == nil || err == windows.ERROR_SUCCESS {
		err = windows.GetLastError()
	}
	return &activityError{operation: operation, err: err}
}

type activityError struct {
	operation string
	err       error
}

func (e *activityError) Error() string { return e.operation + " failed: " + e.err.Error() }
func (e *activityError) Unwrap() error { return e.err }

func (c *controller) Close() error {
	c.closeOnce.Do(func() {
		if c.hwnd != 0 {
			procActivityPostMessage.Call(c.hwnd, wmClose, 0, 0)
		}
		<-c.done
	})
	return nil
}
