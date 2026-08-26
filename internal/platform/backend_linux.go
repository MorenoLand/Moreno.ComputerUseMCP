//go:build linux

package platform

import (
	"context"
	"errors"
	"fmt"
	"image"
	"math/bits"
	"os"
	"strconv"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/jezek/xgb"
	"github.com/jezek/xgb/xproto"
	"github.com/jezek/xgb/xtest"

	"github.com/MorenoLand/Moreno.ComputerUseMCP/internal/computer"
)

const (
	atomActiveWindow  = "_NET_ACTIVE_WINDOW"
	atomClientList    = "_NET_CLIENT_LIST"
	atomNetWmName     = "_NET_WM_NAME"
	atomNetWmState    = "_NET_WM_STATE"
	atomWmStateHidden = "_NET_WM_STATE_HIDDEN"
	atomUTF8String    = "UTF8_STRING"
)

const (
	keysymBackspace = 0xff08
	keysymTab       = 0xff09
	keysymReturn    = 0xff0d
	keysymPause     = 0xff13
	keysymEscape    = 0xff1b
	keysymHome      = 0xff50
	keysymLeft      = 0xff51
	keysymUp        = 0xff52
	keysymRight     = 0xff53
	keysymDown      = 0xff54
	keysymPageUp    = 0xff55
	keysymPageDown  = 0xff56
	keysymEnd       = 0xff57
	keysymPrint     = 0xff61
	keysymInsert    = 0xff63
	keysymMenu      = 0xff67
	keysymNumLock   = 0xff7f
	keysymCapsLock  = 0xffe5
	keysymShift     = 0xffe1
	keysymControl   = 0xffe3
	keysymAlt       = 0xffe9
	keysymSuper     = 0xffeb
	keysymDelete    = 0xffff
	keysymF1        = 0xffbe
	keysymISOLevel3 = 0xfe03
)

type x11Visual struct {
	id    xproto.Visualid
	red   uint32
	green uint32
	blue  uint32
}

type keyBinding struct {
	code  xproto.Keycode
	shift bool
}

type inputEvent struct {
	typ    byte
	detail byte
	x      int16
	y      int16
}

type captureTarget struct {
	drawable xproto.Drawable
	x        int16
	y        int16
	width    int
	height   int
	originX  int
	originY  int
	windowID string
}

type linuxBackend struct {
	conn       *xgb.Conn
	setup      *xproto.SetupInfo
	screen     *xproto.ScreenInfo
	root       xproto.Window
	rootVisual x11Visual
	atoms      map[string]xproto.Atom
	initErr    error
	inputErr   error
	keycodes   map[xproto.Keysym]keyBinding
	closeOnce  sync.Once
}

func newBackend() computer.Backend {
	backend := &linuxBackend{atoms: make(map[string]xproto.Atom)}
	conn, err := xgb.NewConn()
	if err != nil {
		if os.Getenv("WAYLAND_DISPLAY") != "" && os.Getenv("DISPLAY") == "" {
			backend.initErr = fmt.Errorf("%w: native Wayland capture and input are not available yet", computer.ErrUnsupported)
		} else {
			backend.initErr = fmt.Errorf("connect to X11: %w", err)
		}
		return backend
	}
	setup := xproto.Setup(conn)
	if setup == nil || len(setup.Roots) == 0 || conn.DefaultScreen < 0 || conn.DefaultScreen >= len(setup.Roots) {
		conn.Close()
		backend.initErr = errors.New("X11 setup has no usable screen")
		return backend
	}
	screen := setup.DefaultScreen(conn)
	visual, ok := findVisual(setup, screen.RootVisual)
	if !ok {
		conn.Close()
		backend.initErr = errors.New("X11 root visual is unavailable")
		return backend
	}
	backend.conn = conn
	backend.setup = setup
	backend.screen = screen
	backend.root = screen.Root
	backend.rootVisual = visual
	for _, name := range []string{atomActiveWindow, atomClientList, atomNetWmName, atomNetWmState, atomWmStateHidden, atomUTF8String} {
		reply, err := xproto.InternAtom(conn, false, uint16(len(name)), name).Reply()
		if err != nil || reply == nil {
			conn.Close()
			if err == nil {
				err = errors.New("empty atom reply")
			}
			backend.initErr = fmt.Errorf("initialize X11 atom %q: %w", name, err)
			return backend
		}
		backend.atoms[name] = reply.Atom
	}
	if err := xtest.Init(conn); err != nil {
		backend.inputErr = fmt.Errorf("%w: X11 XTEST extension is unavailable: %v", computer.ErrUnsupported, err)
	}
	return backend
}

func (b *linuxBackend) ready() error {
	if b.initErr != nil {
		return b.initErr
	}
	if b.conn == nil {
		return errors.New("X11 backend is not initialized")
	}
	return nil
}

func (b *linuxBackend) readyInput() error {
	if err := b.ready(); err != nil {
		return err
	}
	return b.inputErr
}

func (b *linuxBackend) Screenshot(ctx context.Context, options computer.ScreenshotOptions) (computer.Screenshot, error) {
	if err := ctx.Err(); err != nil {
		return computer.Screenshot{}, err
	}
	if err := b.ready(); err != nil {
		return computer.Screenshot{}, err
	}
	if options.IncludeCursor {
		return computer.Screenshot{}, fmt.Errorf("%w: X11 cursor compositing is not available", computer.ErrUnsupported)
	}
	target, err := b.captureTarget(options)
	if err != nil {
		return computer.Screenshot{}, err
	}
	reply, err := xproto.GetImage(b.conn, xproto.ImageFormatZPixmap, target.drawable, target.x, target.y, uint16(target.width), uint16(target.height), ^uint32(0)).Reply()
	if err != nil {
		return computer.Screenshot{}, fmt.Errorf("capture X11 image: %w", err)
	}
	if reply == nil {
		return computer.Screenshot{}, errors.New("X11 returned an empty image")
	}
	img, err := b.decodeImage(reply, target.width, target.height)
	if err != nil {
		return computer.Screenshot{}, err
	}
	data, mimeType, width, height, err := computer.EncodeImage(img, options)
	if err != nil {
		return computer.Screenshot{}, err
	}
	return computer.Screenshot{Data: data, MIMEType: mimeType, Width: width, Height: height, OriginX: target.originX, OriginY: target.originY, ScaleFactor: 1, Target: options.Target, WindowID: target.windowID}, nil
}

func (b *linuxBackend) ListWindows(ctx context.Context) ([]computer.Window, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := b.ready(); err != nil {
		return nil, err
	}
	ids, err := b.windowIDs()
	if err != nil {
		return nil, err
	}
	windows := make([]computer.Window, 0, len(ids))
	for _, id := range ids {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		window, err := b.windowInfo(id)
		if err == nil && window.Width > 0 && window.Height > 0 {
			windows = append(windows, window)
		}
	}
	return windows, nil
}

func (b *linuxBackend) Move(ctx context.Context, point computer.Point) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := b.readyInput(); err != nil {
		return err
	}
	x, y, err := x11Coordinates(point)
	if err != nil {
		return err
	}
	return b.emit([]inputEvent{{typ: xproto.MotionNotify, x: x, y: y}})
}

func (b *linuxBackend) Click(ctx context.Context, options computer.ClickOptions) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := b.readyInput(); err != nil {
		return err
	}
	x, y, err := x11Coordinates(options.Point)
	if err != nil {
		return err
	}
	button, err := x11Button(options.Button)
	if err != nil {
		return err
	}
	modifiers, err := b.modifierBindings(options.Modifiers)
	if err != nil {
		return err
	}
	events := make([]inputEvent, 0, 1+len(modifiers)*2+options.Clicks*2)
	events = append(events, inputEvent{typ: xproto.MotionNotify, x: x, y: y})
	for _, modifier := range modifiers {
		events = append(events, inputEvent{typ: xproto.KeyPress, detail: byte(modifier.code), x: x, y: y})
	}
	for range options.Clicks {
		events = append(events, inputEvent{typ: xproto.ButtonPress, detail: button, x: x, y: y}, inputEvent{typ: xproto.ButtonRelease, detail: button, x: x, y: y})
	}
	for index := len(modifiers) - 1; index >= 0; index-- {
		events = append(events, inputEvent{typ: xproto.KeyRelease, detail: byte(modifiers[index].code), x: x, y: y})
	}
	return b.emit(events)
}

func (b *linuxBackend) Type(ctx context.Context, options computer.TypeOptions) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := b.readyInput(); err != nil {
		return err
	}
	if !utf8.ValidString(options.Text) {
		return errors.New("X11 typing requires valid UTF-8 text")
	}
	shift, err := b.keyBindingForKeysym(keysymShift)
	if err != nil {
		return err
	}
	events := make([]inputEvent, 0, 512)
	flush := func() error {
		if len(events) == 0 {
			return nil
		}
		err := b.emit(events)
		events = events[:0]
		return err
	}
	for _, r := range options.Text {
		if err := ctx.Err(); err != nil {
			return err
		}
		keysym, ok := x11KeysymForRune(r)
		if !ok {
			return fmt.Errorf("%w: X11 typing does not support rune U+%04X", computer.ErrUnsupported, r)
		}
		binding, err := b.keyBindingForKeysym(keysym)
		if err != nil {
			return err
		}
		if binding.shift {
			events = append(events, inputEvent{typ: xproto.KeyPress, detail: byte(shift.code)}, inputEvent{typ: xproto.KeyPress, detail: byte(binding.code)}, inputEvent{typ: xproto.KeyRelease, detail: byte(binding.code)}, inputEvent{typ: xproto.KeyRelease, detail: byte(shift.code)})
		} else {
			events = append(events, inputEvent{typ: xproto.KeyPress, detail: byte(binding.code)}, inputEvent{typ: xproto.KeyRelease, detail: byte(binding.code)})
		}
		if len(events) >= 512 {
			if err := flush(); err != nil {
				return err
			}
		}
	}
	return flush()
}

func (b *linuxBackend) Keypress(ctx context.Context, options computer.KeypressOptions) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := b.readyInput(); err != nil {
		return err
	}
	keys := expandX11Keys(options.Keys)
	bindings := make([]keyBinding, 0, len(keys))
	for _, key := range keys {
		binding, err := b.keyBindingForKey(key)
		if err != nil {
			return err
		}
		bindings = append(bindings, binding)
	}
	shift, err := b.keyBindingForKeysym(keysymShift)
	if err != nil {
		return err
	}
	events := make([]inputEvent, 0, len(bindings)*2)
	for _, binding := range bindings {
		if binding.shift {
			events = append(events, inputEvent{typ: xproto.KeyPress, detail: byte(shift.code)})
		}
		events = append(events, inputEvent{typ: xproto.KeyPress, detail: byte(binding.code)})
	}
	for index := len(bindings) - 1; index >= 0; index-- {
		binding := bindings[index]
		events = append(events, inputEvent{typ: xproto.KeyRelease, detail: byte(binding.code)})
		if binding.shift {
			events = append(events, inputEvent{typ: xproto.KeyRelease, detail: byte(shift.code)})
		}
	}
	return b.emit(events)
}

func (b *linuxBackend) Close() error {
	b.closeOnce.Do(func() {
		if b.conn != nil {
			b.conn.Close()
		}
	})
	return nil
}

func (b *linuxBackend) captureTarget(options computer.ScreenshotOptions) (captureTarget, error) {
	if options.Target == computer.TargetScreen {
		width := int(b.screen.WidthInPixels)
		height := int(b.screen.HeightInPixels)
		if width <= 0 || height <= 0 {
			return captureTarget{}, errors.New("X11 screen has no captureable area")
		}
		return captureTarget{drawable: xproto.Drawable(b.root), width: width, height: height}, nil
	}
	id := xproto.Window(0)
	var err error
	if options.Target == computer.TargetActiveWindow {
		id, err = b.activeWindow()
	} else {
		id, err = parseWindowID(options.WindowID)
	}
	if err != nil {
		return captureTarget{}, err
	}
	x, y, width, height, _, _, err := b.windowGeometry(id)
	if err != nil {
		return captureTarget{}, err
	}
	if width <= 0 || height <= 0 {
		return captureTarget{}, errors.New("X11 window has no captureable area")
	}
	return captureTarget{drawable: xproto.Drawable(id), width: width, height: height, originX: x, originY: y, windowID: formatWindowID(id)}, nil
}

func (b *linuxBackend) decodeImage(reply *xproto.GetImageReply, width, height int) (*image.RGBA, error) {
	format, ok := b.pixelFormat(reply.Depth)
	if !ok || format.BitsPerPixel == 0 || format.ScanlinePad == 0 {
		return nil, fmt.Errorf("unsupported X11 image depth %d", reply.Depth)
	}
	visual := b.rootVisual
	if reply.Visual != 0 {
		if candidate, ok := findVisual(b.setup, reply.Visual); ok {
			visual = candidate
		}
	}
	bitsPerLine := width * int(format.BitsPerPixel)
	stride := ((bitsPerLine + int(format.ScanlinePad) - 1) / int(format.ScanlinePad)) * int(format.ScanlinePad) / 8
	if stride <= 0 || len(reply.Data) < stride*height {
		return nil, errors.New("X11 image data is shorter than its declared dimensions")
	}
	bytesPerPixel := (int(format.BitsPerPixel) + 7) / 8
	if bytesPerPixel < 1 || bytesPerPixel > 4 {
		return nil, fmt.Errorf("unsupported X11 pixel width %d", format.BitsPerPixel)
	}
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		row := reply.Data[y*stride:]
		for x := 0; x < width; x++ {
			pixel := x11Pixel(row[x*bytesPerPixel:], bytesPerPixel, b.setup.ImageByteOrder)
			target := y*img.Stride + x*4
			img.Pix[target] = scaleX11Channel(pixel, visual.red)
			img.Pix[target+1] = scaleX11Channel(pixel, visual.green)
			img.Pix[target+2] = scaleX11Channel(pixel, visual.blue)
			img.Pix[target+3] = 0xff
		}
	}
	return img, nil
}

func (b *linuxBackend) pixelFormat(depth byte) (xproto.Format, bool) {
	for _, format := range b.setup.PixmapFormats {
		if format.Depth == depth {
			return format, true
		}
	}
	return xproto.Format{}, false
}

func (b *linuxBackend) windowIDs() ([]xproto.Window, error) {
	if atom := b.atoms[atomClientList]; atom != 0 {
		data, err := b.property(b.root, atom, xproto.AtomWindow)
		if err != nil {
			return nil, err
		}
		if len(data) >= 4 {
			ids := make([]xproto.Window, 0, len(data)/4)
			for index := 0; index+4 <= len(data); index += 4 {
				id := xproto.Window(xgb.Get32(data[index:]))
				if id != 0 {
					ids = append(ids, id)
				}
			}
			if len(ids) > 0 {
				return uniqueWindows(ids), nil
			}
		}
	}
	reply, err := xproto.QueryTree(b.conn, b.root).Reply()
	if err != nil {
		return nil, fmt.Errorf("enumerate X11 windows: %w", err)
	}
	if reply == nil {
		return nil, errors.New("X11 returned no window tree")
	}
	return uniqueWindows(reply.Children), nil
}

func (b *linuxBackend) windowInfo(id xproto.Window) (computer.Window, error) {
	x, y, width, height, visible, minimized, err := b.windowGeometry(id)
	if err != nil {
		return computer.Window{}, err
	}
	return computer.Window{ID: formatWindowID(id), Title: b.windowTitle(id), X: x, Y: y, Width: width, Height: height, Visible: visible, Minimized: minimized}, nil
}

func (b *linuxBackend) windowGeometry(id xproto.Window) (int, int, int, int, bool, bool, error) {
	attributes, err := xproto.GetWindowAttributes(b.conn, id).Reply()
	if err != nil || attributes == nil {
		if err == nil {
			err = errors.New("empty attributes reply")
		}
		return 0, 0, 0, 0, false, false, fmt.Errorf("read X11 window attributes %s: %w", formatWindowID(id), err)
	}
	geometry, err := xproto.GetGeometry(b.conn, xproto.Drawable(id)).Reply()
	if err != nil || geometry == nil {
		if err == nil {
			err = errors.New("empty geometry reply")
		}
		return 0, 0, 0, 0, false, false, fmt.Errorf("read X11 window geometry %s: %w", formatWindowID(id), err)
	}
	translated, err := xproto.TranslateCoordinates(b.conn, id, b.root, 0, 0).Reply()
	if err != nil || translated == nil || !translated.SameScreen {
		if err == nil {
			err = errors.New("window is not on the root screen")
		}
		return 0, 0, 0, 0, false, false, fmt.Errorf("translate X11 window geometry %s: %w", formatWindowID(id), err)
	}
	visible := attributes.MapState == xproto.MapStateViewable
	minimized := b.windowIsHidden(id) || (!visible && b.windowStateExists(id))
	return int(translated.DstX), int(translated.DstY), int(geometry.Width), int(geometry.Height), visible, minimized, nil
}

func (b *linuxBackend) windowTitle(id xproto.Window) string {
	data, _ := b.property(id, b.atoms[atomNetWmName], xproto.AtomAny)
	if len(data) == 0 {
		data, _ = b.property(id, xproto.AtomWmName, xproto.AtomString)
	}
	return strings.TrimRight(strings.ToValidUTF8(string(data), ""), string(rune(0)))
}

func (b *linuxBackend) windowIsHidden(id xproto.Window) bool {
	state := b.atoms[atomNetWmState]
	hidden := b.atoms[atomWmStateHidden]
	if state == 0 || hidden == 0 {
		return false
	}
	data, err := b.property(id, state, xproto.AtomAtom)
	if err != nil {
		return false
	}
	for index := 0; index+4 <= len(data); index += 4 {
		if xproto.Atom(xgb.Get32(data[index:])) == hidden {
			return true
		}
	}
	return false
}

func (b *linuxBackend) windowStateExists(id xproto.Window) bool {
	if b.atoms[atomNetWmState] == 0 {
		return false
	}
	data, err := b.property(id, b.atoms[atomNetWmState], xproto.AtomAny)
	return err == nil && len(data) > 0
}

func (b *linuxBackend) activeWindow() (xproto.Window, error) {
	if atom := b.atoms[atomActiveWindow]; atom != 0 {
		data, err := b.property(b.root, atom, xproto.AtomWindow)
		if err != nil {
			return 0, err
		}
		if len(data) >= 4 {
			id := xproto.Window(xgb.Get32(data))
			if id != 0 {
				return id, nil
			}
		}
	}
	focus, err := xproto.GetInputFocus(b.conn).Reply()
	if err != nil || focus == nil || focus.Focus == 0 || focus.Focus == b.root {
		return 0, errors.New("no active X11 window is available")
	}
	return focus.Focus, nil
}

func (b *linuxBackend) property(window xproto.Window, property, typ xproto.Atom) ([]byte, error) {
	if property == 0 {
		return nil, nil
	}
	reply, err := xproto.GetProperty(b.conn, false, window, property, typ, 0, 1<<20).Reply()
	if err != nil {
		return nil, err
	}
	if reply == nil || reply.Type == xproto.AtomNone || reply.ValueLen == 0 {
		return nil, nil
	}
	return reply.Value, nil
}

func (b *linuxBackend) emit(events []inputEvent) error {
	if len(events) == 0 {
		return nil
	}
	for index, event := range events {
		if index == len(events)-1 {
			return xtest.FakeInputChecked(b.conn, event.typ, event.detail, 0, b.root, event.x, event.y, 0).Check()
		}
		xtest.FakeInput(b.conn, event.typ, event.detail, 0, b.root, event.x, event.y, 0)
	}
	return nil
}

func (b *linuxBackend) modifierBindings(modifiers []string) ([]keyBinding, error) {
	bindings := make([]keyBinding, 0, len(modifiers))
	for _, modifier := range modifiers {
		binding, err := b.keyBindingForKey(modifier)
		if err != nil {
			return nil, err
		}
		bindings = append(bindings, binding)
	}
	return bindings, nil
}

func (b *linuxBackend) keyBindingForKey(value string) (keyBinding, error) {
	keysym, err := x11KeysymForKey(value)
	if err != nil {
		return keyBinding{}, err
	}
	return b.keyBindingForKeysym(keysym)
}

func (b *linuxBackend) keyBindingForKeysym(keysym xproto.Keysym) (keyBinding, error) {
	if err := b.loadKeycodes(); err != nil {
		return keyBinding{}, err
	}
	if binding, ok := b.keycodes[keysym]; ok {
		return binding, nil
	}
	return keyBinding{}, fmt.Errorf("X11 key symbol %#x is not available in the current keyboard layout", uint32(keysym))
}

func (b *linuxBackend) loadKeycodes() error {
	if b.keycodes != nil {
		return nil
	}
	count := int(b.setup.MaxKeycode) - int(b.setup.MinKeycode) + 1
	if count <= 0 || count > 255 {
		return errors.New("X11 keyboard mapping has an invalid keycode range")
	}
	reply, err := xproto.GetKeyboardMapping(b.conn, b.setup.MinKeycode, byte(count)).Reply()
	if err != nil {
		return fmt.Errorf("read X11 keyboard mapping: %w", err)
	}
	if reply == nil || reply.KeysymsPerKeycode == 0 {
		return errors.New("X11 keyboard mapping is empty")
	}
	perKeycode := int(reply.KeysymsPerKeycode)
	if len(reply.Keysyms) < count*perKeycode {
		return errors.New("X11 keyboard mapping is truncated")
	}
	keycodes := make(map[xproto.Keysym]keyBinding)
	for index := 0; index < count; index++ {
		code := xproto.Keycode(int(b.setup.MinKeycode) + index)
		for column := 0; column < perKeycode && column < 2; column++ {
			keysym := reply.Keysyms[index*perKeycode+column]
			if keysym != 0 {
				if _, exists := keycodes[keysym]; !exists {
					keycodes[keysym] = keyBinding{code: code, shift: column == 1}
				}
			}
		}
	}
	b.keycodes = keycodes
	return nil
}

func parseWindowID(value string) (xproto.Window, error) {
	parsed, err := strconv.ParseUint(strings.TrimSpace(value), 0, 32)
	if err != nil || parsed == 0 {
		return 0, fmt.Errorf("invalid X11 window id %q", value)
	}
	return xproto.Window(parsed), nil
}

func formatWindowID(id xproto.Window) string {
	return fmt.Sprintf("0x%08x", uint32(id))
}

func uniqueWindows(ids []xproto.Window) []xproto.Window {
	seen := make(map[xproto.Window]struct{}, len(ids))
	result := make([]xproto.Window, 0, len(ids))
	for _, id := range ids {
		if id == 0 {
			continue
		}
		if _, exists := seen[id]; exists {
			continue
		}
		seen[id] = struct{}{}
		result = append(result, id)
	}
	return result
}

func findVisual(setup *xproto.SetupInfo, id xproto.Visualid) (x11Visual, bool) {
	for _, screen := range setup.Roots {
		for _, depth := range screen.AllowedDepths {
			for _, visual := range depth.Visuals {
				if visual.VisualId == id {
					return x11Visual{id: id, red: visual.RedMask, green: visual.GreenMask, blue: visual.BlueMask}, true
				}
			}
		}
	}
	return x11Visual{}, false
}

func x11Pixel(data []byte, bytesPerPixel int, byteOrder byte) uint32 {
	var pixel uint32
	if byteOrder == xproto.ImageOrderMSBFirst {
		for index := 0; index < bytesPerPixel; index++ {
			pixel = pixel<<8 | uint32(data[index])
		}
		return pixel
	}
	for index := 0; index < bytesPerPixel; index++ {
		pixel |= uint32(data[index]) << (8 * index)
	}
	return pixel
}

func scaleX11Channel(pixel, mask uint32) uint8 {
	if mask == 0 {
		return 0
	}
	shift := bits.TrailingZeros32(mask)
	maximum := mask >> shift
	return uint8((uint64((pixel&mask)>>shift) * 255) / uint64(maximum))
}

func x11Coordinates(point computer.Point) (int16, int16, error) {
	if point.X < -32768 || point.X > 32767 || point.Y < -32768 || point.Y > 32767 {
		return 0, 0, fmt.Errorf("X11 pointer coordinate (%d, %d) is outside the protocol range", point.X, point.Y)
	}
	return int16(point.X), int16(point.Y), nil
}

func x11Button(button string) (byte, error) {
	switch strings.ToLower(strings.TrimSpace(button)) {
	case "left":
		return 1, nil
	case "middle":
		return 2, nil
	case "right":
		return 3, nil
	case "x1":
		return 4, nil
	case "x2":
		return 5, nil
	default:
		return 0, fmt.Errorf("invalid mouse button %q", button)
	}
}

func expandX11Keys(keys []string) []string {
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

func x11KeysymForKey(value string) (xproto.Keysym, error) {
	key := strings.ToUpper(strings.TrimSpace(value))
	switch key {
	case "BACKSPACE", "BACK":
		return keysymBackspace, nil
	case "TAB":
		return keysymTab, nil
	case "ENTER", "RETURN":
		return keysymReturn, nil
	case "PAUSE":
		return keysymPause, nil
	case "ESC", "ESCAPE":
		return keysymEscape, nil
	case "SPACE":
		return ' ', nil
	case "PAGEUP", "PAGE_UP":
		return keysymPageUp, nil
	case "PAGEDOWN", "PAGE_DOWN":
		return keysymPageDown, nil
	case "END":
		return keysymEnd, nil
	case "HOME":
		return keysymHome, nil
	case "LEFT", "ARROWLEFT", "ARROW_LEFT":
		return keysymLeft, nil
	case "UP", "ARROWUP", "ARROW_UP":
		return keysymUp, nil
	case "RIGHT", "ARROWRIGHT", "ARROW_RIGHT":
		return keysymRight, nil
	case "DOWN", "ARROWDOWN", "ARROW_DOWN":
		return keysymDown, nil
	case "INSERT", "INS":
		return keysymInsert, nil
	case "DELETE", "DEL":
		return keysymDelete, nil
	case "SHIFT":
		return keysymShift, nil
	case "CTRL", "CONTROL":
		return keysymControl, nil
	case "ALT", "OPTION":
		return keysymAlt, nil
	case "ALTGR", "ALT_GR":
		return keysymISOLevel3, nil
	case "CAPSLOCK", "CAPS_LOCK":
		return keysymCapsLock, nil
	case "NUMLOCK", "NUM_LOCK":
		return keysymNumLock, nil
	case "PRINTSCREEN", "PRINT_SCREEN":
		return keysymPrint, nil
	case "MENU", "APPS", "CONTEXT_MENU":
		return keysymMenu, nil
	case "WIN", "WINDOWS", "META", "SUPER", "COMMAND", "CMD":
		return keysymSuper, nil
	case "GRAVE":
		return xproto.Keysym(0x60), nil
	}
	if len(key) >= 2 && key[0] == 'F' {
		if number, err := strconv.Atoi(key[1:]); err == nil && number >= 1 && number <= 24 {
			return keysymF1 + xproto.Keysym(number-1), nil
		}
	}
	runes := []rune(key)
	if len(runes) == 1 {
		r := runes[0]
		if r >= 'A' && r <= 'Z' {
			r += 'a' - 'A'
		}
		if r >= 0x20 && r <= 0x7e {
			return xproto.Keysym(r), nil
		}
	}
	return 0, fmt.Errorf("unsupported key %q", value)
}

func x11KeysymForRune(r rune) (xproto.Keysym, bool) {
	switch r {
	case '\n', '\r':
		return keysymReturn, true
	case '\t':
		return keysymTab, true
	case '\b':
		return keysymBackspace, true
	}
	if r >= 0x20 && r <= 0x7e {
		return xproto.Keysym(r), true
	}
	return 0, false
}

var _ computer.Backend = (*linuxBackend)(nil)
