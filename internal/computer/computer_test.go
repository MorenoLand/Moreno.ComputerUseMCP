package computer

import (
	"context"
	"errors"
	"testing"
	"time"
)

type backendMock struct {
	screenshot Screenshot
	options    ScreenshotOptions
	click      ClickOptions
	typed      TypeOptions
	keys       KeypressOptions
	point      Point
}

type blockingBackend struct {
	backendMock
	started chan struct{}
}

func (b *blockingBackend) Move(ctx context.Context, point Point) error {
	close(b.started)
	<-ctx.Done()
	return ctx.Err()
}

func (b *backendMock) Screenshot(_ context.Context, options ScreenshotOptions) (Screenshot, error) {
	b.options = options
	return b.screenshot, nil
}

func (b *backendMock) ListWindows(context.Context) ([]Window, error) {
	return []Window{{ID: "window-1", Title: "Test"}}, nil
}

func (b *backendMock) Move(_ context.Context, point Point) error {
	b.point = point
	return nil
}

func (b *backendMock) Click(_ context.Context, options ClickOptions) error {
	b.click = options
	return nil
}

func (b *backendMock) Type(_ context.Context, options TypeOptions) error {
	b.typed = options
	return nil
}

func (b *backendMock) Keypress(_ context.Context, options KeypressOptions) error {
	b.keys = options
	return nil
}

func (b *backendMock) Close() error {
	return nil
}

func TestScreenshotDefaults(t *testing.T) {
	backend := &backendMock{screenshot: Screenshot{Data: []byte("image")}}
	service := NewService(backend)
	_, err := service.Screenshot(context.Background(), ScreenshotOptions{})
	if err != nil {
		t.Fatalf("Screenshot returned error: %v", err)
	}
	if backend.options.Target != TargetScreen {
		t.Fatalf("target = %q, want %q", backend.options.Target, TargetScreen)
	}
	if backend.options.Format != "jpeg" {
		t.Fatalf("format = %q, want jpeg", backend.options.Format)
	}
	if backend.options.Quality != 85 {
		t.Fatalf("quality = %d, want 85", backend.options.Quality)
	}
}

func TestScreenshotRejectsInvalidOptions(t *testing.T) {
	service := NewService(&backendMock{})
	tests := []struct {
		name    string
		options ScreenshotOptions
	}{
		{name: "target", options: ScreenshotOptions{Target: "unknown"}},
		{name: "window id", options: ScreenshotOptions{Target: TargetWindow}},
		{name: "format", options: ScreenshotOptions{Format: "gif"}},
		{name: "quality", options: ScreenshotOptions{Quality: 101}},
		{name: "width", options: ScreenshotOptions{MaxWidth: -1}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := service.Screenshot(context.Background(), test.options); err == nil {
				t.Fatal("Screenshot returned nil error")
			}
		})
	}
}

func TestInputDefaultsAndValidation(t *testing.T) {
	backend := &backendMock{}
	service := NewService(backend)
	if err := service.Click(context.Background(), ClickOptions{Point: Point{X: 4, Y: 7}}); err != nil {
		t.Fatalf("Click returned error: %v", err)
	}
	if backend.click.Button != "left" || backend.click.Clicks != 1 {
		t.Fatalf("click = %#v, want left single click", backend.click)
	}
	if err := service.Type(context.Background(), TypeOptions{Text: "hello"}); err != nil {
		t.Fatalf("Type returned error: %v", err)
	}
	if backend.typed.Text != "hello" {
		t.Fatalf("typed text = %q, want hello", backend.typed.Text)
	}
	if err := service.Keypress(context.Background(), KeypressOptions{}); err == nil {
		t.Fatal("Keypress accepted empty keys")
	}
	if err := service.Click(context.Background(), ClickOptions{Button: "invalid"}); err == nil {
		t.Fatal("Click accepted invalid button")
	}
}

func TestUnsupportedBackend(t *testing.T) {
	backend := UnsupportedBackend{Platform: "test"}
	_, err := backend.Screenshot(context.Background(), ScreenshotOptions{})
	if !errors.Is(err, ErrUnsupported) {
		t.Fatalf("error = %v, want ErrUnsupported", err)
	}
}

func TestAbort(t *testing.T) {
	backend := &blockingBackend{started: make(chan struct{})}
	service := NewService(backend)
	result := make(chan error, 1)
	go func() { result <- service.Move(context.Background(), Point{X: 1, Y: 2}) }()
	select {
	case <-backend.started:
	case <-time.After(time.Second):
		t.Fatal("Move did not start")
	}
	service.Abort()
	select {
	case err := <-result:
		if !errors.Is(err, ErrAborted) {
			t.Fatalf("error = %v, want ErrAborted", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Move did not abort")
	}
}
