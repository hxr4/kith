//go:build !windows

package main

func micaSupported() bool           { return false }
func winDirOnPath(dir string) bool  { return false }
func winAddToPath(dir string) error { return nil }
