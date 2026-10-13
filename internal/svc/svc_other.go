//go:build !darwin && !windows

package svc

import (
	"context"
	"errors"
	"io"
)

var errUnsupported = errors.New("service installation is supported on macOS and Windows only; run `kith daemon` under your init system")

func Install(home, exe string, out io.Writer) error        { return errUnsupported }
func Uninstall(home string, out io.Writer) error           { return errUnsupported }
func Status(home string) (string, error)                   { return "unsupported", nil }
func IsService() bool                                      { return false }
func RunService(run func(ctx context.Context) error) error { return run(context.Background()) }
func FirewallRuleExists() (bool, error)                    { return true, nil }
