//go:build windows

package main

import (
	"path/filepath"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

// micaSupported: Mica needs Windows 11 22H2 (build 22621) or later.
func micaSupported() bool {
	return windows.RtlGetVersion().BuildNumber >= 22621
}

func pathEntries(root registry.Key, key string) []string {
	k, err := registry.OpenKey(root, key, registry.QUERY_VALUE)
	if err != nil {
		return nil
	}
	defer k.Close()
	v, _, err := k.GetStringValue("Path")
	if err != nil {
		return nil
	}
	return strings.Split(v, ";")
}

func samePath(a, b string) bool {
	if x, err := registry.ExpandString(a); err == nil {
		a = x
	}
	a, b = filepath.Clean(a), filepath.Clean(b)
	return strings.EqualFold(strings.TrimRight(a, `\`), strings.TrimRight(b, `\`))
}

// winDirOnPath checks the user and machine PATH in the registry (what a new
// terminal gets), not this process's possibly stale environment.
func winDirOnPath(dir string) bool {
	entries := append(pathEntries(registry.CURRENT_USER, `Environment`),
		pathEntries(registry.LOCAL_MACHINE, `SYSTEM\CurrentControlSet\Control\Session Manager\Environment`)...)
	for _, e := range entries {
		if e != "" && samePath(e, dir) {
			return true
		}
	}
	return false
}

func winAddToPath(dir string) error {
	if winDirOnPath(dir) {
		return nil
	}
	k, _, err := registry.CreateKey(registry.CURRENT_USER, `Environment`, registry.QUERY_VALUE|registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer k.Close()
	cur, _, _ := k.GetStringValue("Path")
	next := dir
	if strings.TrimSpace(cur) != "" {
		next = strings.TrimRight(cur, ";") + ";" + dir
	}
	if err := k.SetExpandStringValue("Path", next); err != nil {
		return err
	}
	// Tell running shells and Explorer that the environment changed.
	env, _ := windows.UTF16PtrFromString("Environment")
	sendMsg := windows.NewLazySystemDLL("user32.dll").NewProc("SendMessageTimeoutW")
	var res uintptr
	sendMsg.Call(0xffff, 0x001A, 0, uintptr(unsafe.Pointer(env)), 0x0002, 3000, uintptr(unsafe.Pointer(&res)))
	return nil
}
