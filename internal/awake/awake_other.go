//go:build !darwin && !linux && !windows

package awake

type unsupported struct{}

func newKeeper() Keeper { return unsupported{} }

func (unsupported) Available() error { return ErrUnsupported }
func (unsupported) Hold(bool) error  { return ErrUnsupported }
func (unsupported) Release()         {}
func (unsupported) Lost() error      { return nil }
