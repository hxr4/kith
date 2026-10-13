//go:build !windows

package main

func winAutostartEnabled() bool                 { return false }
func winSetAutostart(on bool, exe string) error { return nil }
