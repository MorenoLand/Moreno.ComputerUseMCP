//go:build linux

package activity

import (
	"sync"

	"github.com/jezek/xgb"
	"github.com/jezek/xgb/shape"
	"github.com/jezek/xgb/xproto"
)

const (
	activityWidth  = 280
	activityHeight = 44
)

type controller struct {
	abort   func()
	mu      sync.RWMutex
	client  string
	active  bool
	overlay *x11Overlay
	hotkey  *x11Hotkey
	once    sync.Once
}

type x11Overlay struct {
	conn   *xgb.Conn
	window xproto.Window
	bgGC   xproto.Gcontext
	textGC xproto.Gcontext
	font   xproto.Font
	mu     sync.Mutex
	client string
	active bool
	done   chan struct{}
}

type x11Hotkey struct {
	conn    *xgb.Conn
	root    xproto.Window
	keycode xproto.Keycode
	abort   func()
	done    chan struct{}
}

func newController(abort func()) Controller {
	controller := &controller{abort: abort, client: "AI"}
	controller.overlay, _ = newX11Overlay()
	controller.hotkey, _ = newX11Hotkey(abort)
	return controller
}

func (c *controller) Begin() {
	c.mu.Lock()
	c.active = true
	client := c.client
	c.mu.Unlock()
	c.updateOverlay(client, true)
}

func (c *controller) End() {
	c.mu.Lock()
	c.active = false
	client := c.client
	c.mu.Unlock()
	c.updateOverlay(client, false)
}

func (c *controller) SetClient(value string) {
	c.mu.Lock()
	c.client = clientLabel(value)
	client, active := c.client, c.active
	c.mu.Unlock()
	c.updateOverlay(client, active)
}

func (c *controller) updateOverlay(client string, active bool) {
	if c.overlay != nil {
		c.overlay.update(client, active)
	}
}

func (c *controller) Close() error {
	c.once.Do(func() {
		if c.hotkey != nil {
			c.hotkey.close()
		}
		if c.overlay != nil {
			c.overlay.close()
		}
	})
	return nil
}

func newX11Overlay() (*x11Overlay, error) {
	conn, err := xgb.NewConn()
	if err != nil {
		return nil, err
	}
	if err := shape.Init(conn); err != nil {
		conn.Close()
		return nil, err
	}
	setup := xproto.Setup(conn)
	if setup == nil || len(setup.Roots) == 0 || conn.DefaultScreen < 0 || conn.DefaultScreen >= len(setup.Roots) {
		conn.Close()
		return nil, xgb.Errorf("X11 setup has no usable screen")
	}
	screen := setup.DefaultScreen(conn)
	window, err := conn.NewId()
	if err != nil {
		conn.Close()
		return nil, err
	}
	x := int16(int(screen.WidthInPixels) - activityWidth - 16)
	values := []uint32{0x00242424, 1, xproto.EventMaskExposure}
	if err := xproto.CreateWindowChecked(conn, screen.RootDepth, xproto.Window(window), screen.Root, x, 16, activityWidth, activityHeight, 0, xproto.WindowClassInputOutput, screen.RootVisual, xproto.CwBackPixel|xproto.CwOverrideRedirect|xproto.CwEventMask, values).Check(); err != nil {
		conn.Close()
		return nil, err
	}
	if err := shape.RectanglesChecked(conn, shape.SoSet, shape.SkInput, 0, xproto.Window(window), 0, 0, nil).Check(); err != nil {
		xproto.DestroyWindow(conn, xproto.Window(window))
		conn.Close()
		return nil, err
	}
	fontID, err := conn.NewId()
	if err != nil {
		xproto.DestroyWindow(conn, xproto.Window(window))
		conn.Close()
		return nil, err
	}
	if err := xproto.OpenFontChecked(conn, xproto.Font(fontID), uint16(len("fixed")), "fixed").Check(); err != nil {
		xproto.DestroyWindow(conn, xproto.Window(window))
		conn.Close()
		return nil, err
	}
	bgGCID, err := conn.NewId()
	if err != nil {
		xproto.CloseFont(conn, xproto.Font(fontID))
		xproto.DestroyWindow(conn, xproto.Window(window))
		conn.Close()
		return nil, err
	}
	if err := xproto.CreateGCChecked(conn, xproto.Gcontext(bgGCID), xproto.Drawable(window), xproto.GcForeground, []uint32{0x00242424}).Check(); err != nil {
		xproto.CloseFont(conn, xproto.Font(fontID))
		xproto.DestroyWindow(conn, xproto.Window(window))
		conn.Close()
		return nil, err
	}
	textGCID, err := conn.NewId()
	if err != nil {
		xproto.FreeGC(conn, xproto.Gcontext(bgGCID))
		xproto.CloseFont(conn, xproto.Font(fontID))
		xproto.DestroyWindow(conn, xproto.Window(window))
		conn.Close()
		return nil, err
	}
	if err := xproto.CreateGCChecked(conn, xproto.Gcontext(textGCID), xproto.Drawable(window), xproto.GcForeground|xproto.GcFont, []uint32{0x00FFFFFF, uint32(fontID)}).Check(); err != nil {
		xproto.FreeGC(conn, xproto.Gcontext(bgGCID))
		xproto.CloseFont(conn, xproto.Font(fontID))
		xproto.DestroyWindow(conn, xproto.Window(window))
		conn.Close()
		return nil, err
	}
	overlay := &x11Overlay{conn: conn, window: xproto.Window(window), bgGC: xproto.Gcontext(bgGCID), textGC: xproto.Gcontext(textGCID), font: xproto.Font(fontID), client: "AI", done: make(chan struct{})}
	xproto.ConfigureWindow(conn, overlay.window, xproto.ConfigWindowStackMode, []uint32{xproto.StackModeAbove})
	xproto.MapWindow(conn, overlay.window)
	overlay.redraw()
	conn.Sync()
	go overlay.events()
	return overlay, nil
}

func (o *x11Overlay) update(client string, active bool) {
	o.mu.Lock()
	o.client = client
	o.active = active
	o.redrawLocked()
	o.mu.Unlock()
}

func (o *x11Overlay) redraw() {
	o.mu.Lock()
	o.redrawLocked()
	o.mu.Unlock()
}

func (o *x11Overlay) redrawLocked() {
	background := uint32(0x00242424)
	stripe := uint32(0x003D9BEA)
	if o.active {
		stripe = 0x0000A5FF
	}
	xproto.ChangeGC(o.conn, o.bgGC, xproto.GcForeground, []uint32{background})
	xproto.PolyFillRectangle(o.conn, xproto.Drawable(o.window), o.bgGC, []xproto.Rectangle{{Width: activityWidth, Height: activityHeight}})
	xproto.ChangeGC(o.conn, o.bgGC, xproto.GcForeground, []uint32{stripe})
	xproto.PolyFillRectangle(o.conn, xproto.Drawable(o.window), o.bgGC, []xproto.Rectangle{{Width: activityWidth, Height: 3}})
	xproto.ImageText8(o.conn, uint8(len(activityASCII("Computer controlled by "+o.client))), xproto.Drawable(o.window), o.textGC, 10, 17, activityASCII("Computer controlled by "+o.client))
	xproto.ChangeGC(o.conn, o.textGC, xproto.GcForeground, []uint32{0x00D0D0D0})
	stop := activityASCII("Esc to stop")
	xproto.ImageText8(o.conn, uint8(len(stop)), xproto.Drawable(o.window), o.textGC, 10, 34, stop)
	xproto.ChangeGC(o.conn, o.textGC, xproto.GcForeground, []uint32{0x00FFFFFF})
	xproto.ConfigureWindow(o.conn, o.window, xproto.ConfigWindowStackMode, []uint32{xproto.StackModeAbove})
	o.conn.Sync()
}

func (o *x11Overlay) events() {
	defer close(o.done)
	for {
		event, err := o.conn.WaitForEvent()
		if err != nil || event == nil {
			return
		}
		if _, ok := event.(xproto.ExposeEvent); ok {
			o.redraw()
		}
	}
}

func (o *x11Overlay) close() {
	xproto.DestroyWindow(o.conn, o.window)
	xproto.FreeGC(o.conn, o.bgGC)
	xproto.FreeGC(o.conn, o.textGC)
	xproto.CloseFont(o.conn, o.font)
	o.conn.Sync()
	o.conn.Close()
	<-o.done
}

func newX11Hotkey(abort func()) (*x11Hotkey, error) {
	conn, err := xgb.NewConn()
	if err != nil {
		return nil, err
	}
	setup := xproto.Setup(conn)
	if setup == nil || len(setup.Roots) == 0 || conn.DefaultScreen < 0 || conn.DefaultScreen >= len(setup.Roots) {
		conn.Close()
		return nil, xgb.Errorf("X11 setup has no usable screen")
	}
	screen := setup.DefaultScreen(conn)
	keycode, err := x11Keycode(conn, setup, keysymEscape)
	if err != nil {
		conn.Close()
		return nil, err
	}
	if err := xproto.GrabKeyChecked(conn, true, screen.Root, 0, keycode, xproto.GrabModeAsync, xproto.GrabModeAsync).Check(); err != nil {
		conn.Close()
		return nil, err
	}
	conn.Sync()
	hotkey := &x11Hotkey{conn: conn, root: screen.Root, keycode: keycode, abort: abort, done: make(chan struct{})}
	go hotkey.events()
	return hotkey, nil
}

func (h *x11Hotkey) events() {
	defer close(h.done)
	for {
		event, err := h.conn.WaitForEvent()
		if err != nil || event == nil {
			return
		}
		key, ok := event.(xproto.KeyPressEvent)
		if !ok || key.Detail != h.keycode {
			continue
		}
		if h.abort != nil {
			h.abort()
		}
		xproto.AllowEvents(h.conn, xproto.AllowReplayKeyboard, xproto.TimeCurrentTime)
		h.conn.Sync()
	}
}

func (h *x11Hotkey) close() {
	xproto.UngrabKey(h.conn, h.keycode, h.root, 0)
	h.conn.Sync()
	h.conn.Close()
	<-h.done
}

func x11Keycode(conn *xgb.Conn, setup *xproto.SetupInfo, target xproto.Keysym) (xproto.Keycode, error) {
	count := int(setup.MaxKeycode) - int(setup.MinKeycode) + 1
	if count <= 0 || count > 255 {
		return 0, xgb.Errorf("X11 keyboard mapping has an invalid keycode range")
	}
	reply, err := xproto.GetKeyboardMapping(conn, setup.MinKeycode, byte(count)).Reply()
	if err != nil {
		return 0, err
	}
	if reply == nil || reply.KeysymsPerKeycode == 0 {
		return 0, xgb.Errorf("X11 keyboard mapping is empty")
	}
	perKeycode := int(reply.KeysymsPerKeycode)
	for index := 0; index < count; index++ {
		for column := 0; column < perKeycode; column++ {
			if reply.Keysyms[index*perKeycode+column] == target {
				return xproto.Keycode(int(setup.MinKeycode) + index), nil
			}
		}
	}
	return 0, xgb.Errorf("X11 Escape key is not available")
}

func activityASCII(value string) string {
	runes := []rune(value)
	for index, r := range runes {
		if r < 0x20 || r > 0x7e {
			runes[index] = '?'
		}
	}
	return string(runes)
}

const keysymEscape xproto.Keysym = 0xff1b
