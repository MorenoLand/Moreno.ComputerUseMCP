//go:build linux

package platform

import (
	"testing"

	"github.com/jezek/xgb/xproto"
)

func TestX11Pixel(t *testing.T) {
	if got := x11Pixel([]byte{0x12, 0x34, 0x56, 0x78}, 4, xproto.ImageOrderLSBFirst); got != 0x78563412 {
		t.Fatalf("little-endian pixel = %#x", got)
	}
	if got := x11Pixel([]byte{0x12, 0x34, 0x56, 0x78}, 4, xproto.ImageOrderMSBFirst); got != 0x12345678 {
		t.Fatalf("big-endian pixel = %#x", got)
	}
}

func TestScaleX11Channel(t *testing.T) {
	if got := scaleX11Channel(0x0000ff00, 0x0000ff00); got != 255 {
		t.Fatalf("full channel = %d", got)
	}
	if got := scaleX11Channel(0x00008000, 0x0000ff00); got != 128 {
		t.Fatalf("half channel = %d", got)
	}
}

func TestX11KeyAliases(t *testing.T) {
	if got, err := x11KeysymForKey("Ctrl"); err != nil || got != keysymControl {
		t.Fatalf("ctrl = %#x, %v", got, err)
	}
	if got, err := x11KeysymForKey("A"); err != nil || got != xproto.Keysym('a') {
		t.Fatalf("A = %#x, %v", got, err)
	}
	if got, ok := x11KeysymForRune('A'); !ok || got != xproto.Keysym('A') {
		t.Fatalf("typed A = %#x, %t", got, ok)
	}
}
