//go:build !darwin && !linux && !windows

package awake

import "context"

type noLid struct{}

func newLid(string) Lid { return noLid{} }

func (noLid) Available() error            { return ErrUnsupported }
func (noLid) Setup(context.Context) error { return ErrUnsupported }
func (noLid) Hold() error                 { return ErrUnsupported }
func (noLid) Release() error              { return nil }
func (noLid) Recover() error              { return nil }

func readPower() Power { return Power{} }
