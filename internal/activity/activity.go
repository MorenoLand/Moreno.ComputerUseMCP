package activity

import (
	"strings"
	"sync"
	"time"
)

type Controller interface {
	Begin()
	End()
	SetClient(string)
	Close() error
}

const activityHold = 2 * time.Second

type heldController struct {
	native     Controller
	mu         sync.Mutex
	timer      *time.Timer
	generation uint64
	closed     bool
}

func New(abort func()) Controller { return &heldController{native: newController(abort)} }

func (c *heldController) Begin() {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return
	}
	c.generation++
	if c.timer != nil {
		c.timer.Stop()
		c.timer = nil
	}
	c.mu.Unlock()
	c.native.Begin()
}

func (c *heldController) End() {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return
	}
	c.generation++
	generation := c.generation
	if c.timer != nil {
		c.timer.Stop()
	}
	c.timer = time.AfterFunc(activityHold, func() {
		c.mu.Lock()
		if c.closed || generation != c.generation {
			c.mu.Unlock()
			return
		}
		c.timer = nil
		c.native.End()
		c.mu.Unlock()
	})
	c.mu.Unlock()
}

func (c *heldController) SetClient(value string) { c.native.SetClient(value) }

func (c *heldController) Close() error {
	c.mu.Lock()
	c.closed = true
	c.generation++
	if c.timer != nil {
		c.timer.Stop()
		c.timer = nil
	}
	c.mu.Unlock()
	return c.native.Close()
}

func clientLabel(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "AI"
	}
	value = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, value)
	if value == "" {
		return "AI"
	}
	runes := []rune(value)
	if len(runes) > 24 {
		value = string(runes[:24]) + "…"
	}
	return value
}
