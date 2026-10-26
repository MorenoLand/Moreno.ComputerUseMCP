//go:build windows

package platform

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/MorenoLand/Moreno.ComputerUseMCP/internal/computer"
)

// TestScreenshotMinimizedWindow captures the minimized window whose title
// contains MINIMIZED_WINDOW_TITLE and checks it is minimized again afterwards.
// Set MINIMIZED_WINDOW_OUT to keep the captured PNG.
func TestScreenshotMinimizedWindow(t *testing.T) {
	title := os.Getenv("MINIMIZED_WINDOW_TITLE")
	if title == "" {
		t.Skip("MINIMIZED_WINDOW_TITLE is not set")
	}
	backend := newBackend()
	windows, err := backend.ListWindows(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var target computer.Window
	for _, window := range windows {
		if strings.Contains(window.Title, title) {
			target = window
			break
		}
	}
	if target.ID == "" || !target.Minimized {
		t.Fatalf("no minimized window matching %q in %+v", title, windows)
	}
	if target.Width < 100 || target.Height < 100 {
		t.Fatalf("listed size %dx%d looks like the parked icon", target.Width, target.Height)
	}
	hwnd, err := parseWindowID(target.ID)
	if err != nil {
		t.Fatal(err)
	}
	before, _ := getWindowPlacement(hwnd)
	shot, err := backend.Screenshot(context.Background(), computer.ScreenshotOptions{Target: computer.TargetWindow, WindowID: target.ID, Format: "png", Quality: 85})
	if err != nil {
		t.Fatal(err)
	}
	// Graphics capture excludes the invisible resize border GetWindowRect includes.
	if target.Width-shot.Width > 24 || target.Height-shot.Height > 24 || shot.Width > target.Width || shot.Height > target.Height {
		t.Errorf("captured %dx%d, listed %dx%d", shot.Width, shot.Height, target.Width, target.Height)
	}
	if !isMinimized(hwnd) {
		t.Error("window was not minimized again after capture")
	}
	after, _ := getWindowPlacement(hwnd)
	if before.NormalPosition != after.NormalPosition || before.Flags != after.Flags {
		t.Errorf("placement changed: %+v -> %+v", before, after)
	}
	if out := os.Getenv("MINIMIZED_WINDOW_OUT"); out != "" {
		if err := os.WriteFile(out, shot.Data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
}
