package computer

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
)

var ErrUnsupported = errors.New("computer use is unsupported")
var ErrAborted = errors.New("computer use aborted by Escape")

type Target string

const (
	TargetScreen       Target = "screen"
	TargetActiveWindow Target = "active_window"
	TargetWindow       Target = "window"
)

type ScreenshotOptions struct {
	Target        Target
	WindowID      string
	Format        string
	Quality       int
	MaxWidth      int
	IncludeCursor bool
}

type Screenshot struct {
	Data        []byte
	MIMEType    string
	Width       int
	Height      int
	OriginX     int
	OriginY     int
	ScaleFactor float64
	Target      Target
	WindowID    string
}

type Window struct {
	ID        string `json:"id"`
	Title     string `json:"title"`
	X         int    `json:"x"`
	Y         int    `json:"y"`
	Width     int    `json:"width"`
	Height    int    `json:"height"`
	Visible   bool   `json:"visible"`
	Minimized bool   `json:"minimized"`
}

type Point struct {
	X int
	Y int
}

type ClickOptions struct {
	Point     Point
	Button    string
	Clicks    int
	Modifiers []string
}

type TypeOptions struct {
	Text string
}

type KeypressOptions struct {
	Keys []string
}

type Backend interface {
	Screenshot(context.Context, ScreenshotOptions) (Screenshot, error)
	ListWindows(context.Context) ([]Window, error)
	Move(context.Context, Point) error
	Click(context.Context, ClickOptions) error
	Type(context.Context, TypeOptions) error
	Keypress(context.Context, KeypressOptions) error
	Close() error
}

type Service struct {
	backend  Backend
	mu       sync.Mutex
	activeMu sync.Mutex
	activeOp *operation
}

type operation struct {
	cancel  context.CancelFunc
	aborted bool
}

func NewService(backend Backend) *Service {
	if backend == nil {
		panic("nil computer backend")
	}
	return &Service{backend: backend}
}

func (s *Service) Screenshot(ctx context.Context, options ScreenshotOptions) (Screenshot, error) {
	if err := normalizeScreenshotOptions(&options); err != nil {
		return Screenshot{}, err
	}
	var result Screenshot
	err := s.run(ctx, func(ctx context.Context) error {
		var err error
		result, err = s.backend.Screenshot(ctx, options)
		return err
	})
	return result, err
}

func (s *Service) ListWindows(ctx context.Context) ([]Window, error) {
	var result []Window
	err := s.run(ctx, func(ctx context.Context) error {
		var err error
		result, err = s.backend.ListWindows(ctx)
		return err
	})
	return result, err
}

func (s *Service) Move(ctx context.Context, point Point) error {
	return s.run(ctx, func(ctx context.Context) error { return s.backend.Move(ctx, point) })
}

func (s *Service) Click(ctx context.Context, options ClickOptions) error {
	if err := normalizeClickOptions(&options); err != nil {
		return err
	}
	return s.run(ctx, func(ctx context.Context) error { return s.backend.Click(ctx, options) })
}

func (s *Service) Type(ctx context.Context, options TypeOptions) error {
	if len(options.Text) > 1<<20 {
		return errors.New("typed text exceeds 1 MiB")
	}
	return s.run(ctx, func(ctx context.Context) error { return s.backend.Type(ctx, options) })
}

func (s *Service) Keypress(ctx context.Context, options KeypressOptions) error {
	if len(options.Keys) == 0 {
		return errors.New("at least one key is required")
	}
	if len(options.Keys) > 16 {
		return errors.New("at most 16 keys may be sent in one keypress")
	}
	return s.run(ctx, func(ctx context.Context) error { return s.backend.Keypress(ctx, options) })
}

func (s *Service) Abort() {
	s.activeMu.Lock()
	defer s.activeMu.Unlock()
	if s.activeOp != nil {
		s.activeOp.aborted = true
		s.activeOp.cancel()
	}
}

func (s *Service) run(ctx context.Context, fn func(context.Context) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	operationCtx, cancel := context.WithCancel(ctx)
	op := &operation{cancel: cancel}
	s.activeMu.Lock()
	s.activeOp = op
	s.activeMu.Unlock()
	err := fn(operationCtx)
	s.activeMu.Lock()
	if s.activeOp == op {
		s.activeOp = nil
	}
	aborted := op.aborted
	s.activeMu.Unlock()
	cancel()
	if aborted {
		return ErrAborted
	}
	return err
}

func (s *Service) Close() error {
	s.Abort()
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.backend.Close()
}

func normalizeScreenshotOptions(options *ScreenshotOptions) error {
	if options.Target == "" {
		options.Target = TargetScreen
	}
	switch options.Target {
	case TargetScreen, TargetActiveWindow:
	case TargetWindow:
		if strings.TrimSpace(options.WindowID) == "" {
			return errors.New("window_id is required for window screenshots")
		}
	default:
		return fmt.Errorf("invalid screenshot target %q", options.Target)
	}
	if options.Format == "" {
		options.Format = "jpeg"
	}
	if options.Format != "jpeg" && options.Format != "png" {
		return fmt.Errorf("invalid screenshot format %q", options.Format)
	}
	if options.Quality == 0 {
		options.Quality = 85
	}
	if options.Quality < 1 || options.Quality > 100 {
		return errors.New("screenshot quality must be between 1 and 100")
	}
	if options.MaxWidth < 0 || options.MaxWidth > 16384 {
		return errors.New("max_width must be between 0 and 16384")
	}
	return nil
}

func normalizeClickOptions(options *ClickOptions) error {
	if options.Button == "" {
		options.Button = "left"
	}
	switch options.Button {
	case "left", "right", "middle", "x1", "x2":
	default:
		return fmt.Errorf("invalid mouse button %q", options.Button)
	}
	if options.Clicks == 0 {
		options.Clicks = 1
	}
	if options.Clicks < 1 || options.Clicks > 3 {
		return errors.New("clicks must be between 1 and 3")
	}
	if len(options.Modifiers) > 4 {
		return errors.New("at most four click modifiers are supported")
	}
	return nil
}

type UnsupportedBackend struct {
	Platform string
}

func (b UnsupportedBackend) Screenshot(context.Context, ScreenshotOptions) (Screenshot, error) {
	return Screenshot{}, fmt.Errorf("%w on %s", ErrUnsupported, b.Platform)
}

func (b UnsupportedBackend) ListWindows(context.Context) ([]Window, error) {
	return nil, fmt.Errorf("%w on %s", ErrUnsupported, b.Platform)
}

func (b UnsupportedBackend) Move(context.Context, Point) error {
	return fmt.Errorf("%w on %s", ErrUnsupported, b.Platform)
}

func (b UnsupportedBackend) Click(context.Context, ClickOptions) error {
	return fmt.Errorf("%w on %s", ErrUnsupported, b.Platform)
}

func (b UnsupportedBackend) Type(context.Context, TypeOptions) error {
	return fmt.Errorf("%w on %s", ErrUnsupported, b.Platform)
}

func (b UnsupportedBackend) Keypress(context.Context, KeypressOptions) error {
	return fmt.Errorf("%w on %s", ErrUnsupported, b.Platform)
}

func (b UnsupportedBackend) Close() error {
	return nil
}
