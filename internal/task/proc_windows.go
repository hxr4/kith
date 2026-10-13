//go:build windows

package task

import (
	"fmt"
	"os/exec"
	"runtime"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	ntdll                       = windows.NewLazySystemDLL("ntdll.dll")
	procNtResumeProcess         = ntdll.NewProc("NtResumeProcess")
	kernel32                    = windows.NewLazySystemDLL("kernel32.dll")
	procSetThreadExecutionState = kernel32.NewProc("SetThreadExecutionState")
)

const createNoWindow = 0x08000000

type treeHandle struct {
	mu  sync.Mutex
	job windows.Handle
}

// startInTree starts the child suspended, assigns it to a Job Object with
// KILL_ON_JOB_CLOSE, then resumes it, so no descendant can escape the job.
func startInTree(cmd *exec.Cmd) (*treeHandle, error) {
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return nil, fmt.Errorf("CreateJobObject: %w", err)
	}
	info := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{
		BasicLimitInformation: windows.JOBOBJECT_BASIC_LIMIT_INFORMATION{LimitFlags: windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE},
	}
	if _, err := windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info))); err != nil {
		windows.CloseHandle(job)
		return nil, fmt.Errorf("SetInformationJobObject: %w", err)
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_SUSPENDED | windows.CREATE_NEW_PROCESS_GROUP | createNoWindow}
	if err := cmd.Start(); err != nil {
		windows.CloseHandle(job)
		return nil, err
	}
	h, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE|windows.PROCESS_SUSPEND_RESUME, false, uint32(cmd.Process.Pid))
	if err != nil {
		cmd.Process.Kill()
		windows.CloseHandle(job)
		return nil, fmt.Errorf("OpenProcess: %w", err)
	}
	defer windows.CloseHandle(h)
	if err := windows.AssignProcessToJobObject(job, h); err != nil {
		cmd.Process.Kill()
		windows.CloseHandle(job)
		return nil, fmt.Errorf("AssignProcessToJobObject: %w", err)
	}
	if r, _, _ := procNtResumeProcess.Call(uintptr(h)); r != 0 {
		windows.TerminateJobObject(job, 1)
		windows.CloseHandle(job)
		return nil, fmt.Errorf("NtResumeProcess: status 0x%x", r)
	}
	return &treeHandle{job: job}, nil
}

func (t *treeHandle) terminate(done <-chan struct{}, _ time.Duration) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.job != 0 {
		windows.TerminateJobObject(t.job, 1)
	}
}

func (t *treeHandle) cleanup() {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.job != 0 {
		windows.TerminateJobObject(t.job, 1)
		windows.CloseHandle(t.job)
		t.job = 0
	}
}

// KillOrphan is a no-op on Windows: KILL_ON_JOB_CLOSE already killed the
// tree when the crashed daemon's job handle was closed by the OS.
func KillOrphan(pid int) {}

const (
	esContinuous     = 0x80000000
	esSystemRequired = 0x00000001
)

// PreventSleep holds SetThreadExecutionState on a locked OS thread until
// released.
func PreventSleep(pid int) func() {
	stop := make(chan struct{})
	go func() {
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		procSetThreadExecutionState.Call(esContinuous | esSystemRequired)
		<-stop
		procSetThreadExecutionState.Call(esContinuous)
	}()
	var once sync.Once
	return func() { once.Do(func() { close(stop) }) }
}

// Alive reports whether pid is a running process.
func Alive(pid int) bool {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return false
	}
	defer windows.CloseHandle(h)
	var code uint32
	if err := windows.GetExitCodeProcess(h, &code); err != nil {
		return false
	}
	return code == 259 // STILL_ACTIVE
}
