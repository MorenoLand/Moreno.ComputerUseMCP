//go:build !windows && !linux

package activity

type controller struct{}

func newController(func()) Controller { return controller{} }
func (controller) Begin()             {}
func (controller) End()               {}
func (controller) SetClient(string)   {}
func (controller) Close() error       { return nil }
