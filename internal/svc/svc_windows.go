//go:build windows

// Package svc installs Kith as an OS service: a LaunchAgent on macOS and a
// Windows service (plus firewall rule) on Windows.
package svc

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/user"
	"strings"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/mgr"
)

const (
	ServiceName  = "kith"
	RuleName     = "Kith (TCP 7420)"
	MDNSRuleName = "Kith (mDNS UDP 5353)"
)

func isAdmin() bool {
	var sid *windows.SID
	err := windows.AllocateAndInitializeSid(&windows.SECURITY_NT_AUTHORITY, 2, windows.SECURITY_BUILTIN_DOMAIN_RID,
		windows.DOMAIN_ALIAS_RID_ADMINS, 0, 0, 0, 0, 0, 0, &sid)
	if err != nil {
		return false
	}
	defer windows.FreeSid(sid)
	ok, err := windows.Token(0).IsMember(sid)
	return err == nil && ok
}

// readPassword reads a line from the console without echo.
func readPassword(prompt string, out io.Writer) (string, error) {
	fmt.Fprint(out, prompt)
	h := windows.Handle(os.Stdin.Fd())
	var mode uint32
	if err := windows.GetConsoleMode(h, &mode); err == nil {
		windows.SetConsoleMode(h, mode&^windows.ENABLE_ECHO_INPUT)
		defer windows.SetConsoleMode(h, mode)
	}
	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
	fmt.Fprintln(out)
	return strings.TrimRight(line, "\r\n"), err
}

// Install registers the Windows service to run as the current user (tasks
// run "as your user", §6), grants that user the logon-as-service right,
// adds the Private-profile firewall rule and starts the service.
func Install(home, exe string, out io.Writer) error {
	if !isAdmin() {
		return errors.New("`kith daemon install` must run in an elevated (Administrator) terminal")
	}
	u, err := user.Current()
	if err != nil {
		return err
	}
	account := u.Username // DOMAIN\user or MACHINE\user
	fmt.Fprintf(out, "The Kith service runs tasks as %s, so Windows needs that account's password\n", account)
	fmt.Fprintln(out, "(stored by the Service Control Manager, never by Kith).")
	pw, err := readPassword("Password for "+account+": ", out)
	if err != nil {
		return err
	}
	if err := grantServiceLogonRight(u.Uid); err != nil {
		return fmt.Errorf("grant 'Log on as a service' to %s: %w", account, err)
	}
	m, err := mgr.Connect()
	if err != nil {
		return err
	}
	defer m.Disconnect()
	if s, err := m.OpenService(ServiceName); err == nil {
		s.Control(svc.Stop)
		time.Sleep(2 * time.Second)
		s.Delete()
		s.Close()
		time.Sleep(1 * time.Second)
	}
	s, err := m.CreateService(ServiceName, exe, mgr.Config{
		DisplayName:      "Kith task fabric",
		Description:      "Kith daemon: local-first task delegation between paired machines.",
		StartType:        mgr.StartAutomatic,
		ServiceStartName: account,
		Password:         pw,
	}, "daemon", "--service", "--home", home)
	if err != nil {
		return fmt.Errorf("create service: %w", err)
	}
	defer s.Close()
	s.SetRecoveryActions([]mgr.RecoveryAction{{Type: mgr.ServiceRestart, Delay: 5 * time.Second}}, 86400)
	// Services do not see per-user PATH entries (e.g. a per-user Python);
	// give the service the installing user's PATH so argv[0] resolves.
	if k, err := registry.OpenKey(registry.LOCAL_MACHINE, `SYSTEM\CurrentControlSet\Services\`+ServiceName, registry.SET_VALUE); err == nil {
		k.SetStringsValue("Environment", []string{"PATH=" + os.Getenv("PATH")})
		k.Close()
		fmt.Fprintln(out, "Tasks will see this terminal's PATH; re-run `kith daemon install` after installing new tools.")
	}
	if err := addFirewallRule(exe); err != nil {
		fmt.Fprintf(out, "WARNING: could not add firewall rule: %v\n", err)
		fmt.Fprintf(out, "Add it manually: netsh advfirewall firewall add rule name=\"%s\" dir=in action=allow protocol=TCP localport=7420 profile=private\n", RuleName)
	} else {
		fmt.Fprintf(out, "Firewall: allowed inbound TCP 7420 and mDNS (UDP 5353) for kith.exe on the Private profile.\n")
	}
	if err := s.Start(); err != nil {
		return fmt.Errorf("start service: %w (check that the password is correct)", err)
	}
	fmt.Fprintf(out, "Installed and started Windows service %q (automatic start).\n", ServiceName)
	fmt.Fprintf(out, "Daemon log: %s\\daemon.log\n\n", home)
	fmt.Fprintln(out, "Manual step: make sure this network is set to Private")
	fmt.Fprintln(out, "(Settings > Network & internet > your network > Private network); the firewall rule only applies there.")
	return nil
}

func addFirewallRule(exe string) error {
	for _, r := range []struct{ name, proto, port string }{{RuleName, "TCP", "7420"}, {MDNSRuleName, "UDP", "5353"}} {
		exec.Command("netsh", "advfirewall", "firewall", "delete", "rule", "name="+r.name).Run()
		// Windows' built-in mDNS rule only covers the DNS Client service, so
		// kith.exe needs its own inbound UDP 5353 rule to answer and hear
		// mDNS (DECISIONS.md D-045).
		b, err := exec.Command("netsh", "advfirewall", "firewall", "add", "rule", "name="+r.name, "dir=in", "action=allow",
			"protocol="+r.proto, "localport="+r.port, "profile=private", "program="+exe, "enable=yes").CombinedOutput()
		if err != nil {
			return fmt.Errorf("%s: %v: %s", r.name, err, strings.TrimSpace(string(b)))
		}
	}
	return nil
}

// AddFirewallRules adds (or replaces) Kith's inbound Private-profile rules
// for TCP 7420 and mDNS UDP 5353. Needs an elevated process.
func AddFirewallRules(exe string) error {
	if !isAdmin() {
		return fmt.Errorf("adding firewall rules needs an Administrator: run this from an elevated terminal")
	}
	return addFirewallRule(exe)
}

// NetworkProfiles returns "name=category" for each connected network
// (category Public, Private or DomainAuthenticated).
func NetworkProfiles() ([]string, error) {
	cmd := exec.Command("powershell", "-NoProfile", "-NonInteractive", "-Command",
		`Get-NetConnectionProfile | ForEach-Object { $_.Name + '=' + $_.NetworkCategory }`)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000} // CREATE_NO_WINDOW
	b, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	var out []string
	for _, l := range strings.Split(strings.ReplaceAll(string(b), "\r", ""), "\n") {
		if l = strings.TrimSpace(l); l != "" {
			out = append(out, l)
		}
	}
	return out, nil
}

func FirewallRuleExists() (bool, error) {
	err := exec.Command("netsh", "advfirewall", "firewall", "show", "rule", "name="+RuleName).Run()
	return err == nil, nil
}

// MDNSRuleExists reports whether the kith.exe mDNS rule is present.
func MDNSRuleExists() bool {
	return exec.Command("netsh", "advfirewall", "firewall", "show", "rule", "name="+MDNSRuleName).Run() == nil
}

func Uninstall(home string, out io.Writer) error {
	if !isAdmin() {
		return errors.New("`kith daemon uninstall` must run in an elevated (Administrator) terminal")
	}
	m, err := mgr.Connect()
	if err != nil {
		return err
	}
	defer m.Disconnect()
	if s, err := m.OpenService(ServiceName); err == nil {
		s.Control(svc.Stop)
		time.Sleep(2 * time.Second)
		if err := s.Delete(); err != nil {
			s.Close()
			return err
		}
		s.Close()
	}
	exec.Command("netsh", "advfirewall", "firewall", "delete", "rule", "name="+RuleName).Run()
	exec.Command("netsh", "advfirewall", "firewall", "delete", "rule", "name="+MDNSRuleName).Run()
	fmt.Fprintln(out, "Service and firewall rules removed. Your identity, peers and tasks in", home, "are kept.")
	return nil
}

// openService opens the kith service with only the rights asked for.
// mgr.Connect requests SC_MANAGER_ALL_ACCESS, which non-elevated users do
// not have; querying needs only SC_MANAGER_CONNECT plus query rights, which
// the default service DACL grants to interactive users.
func openService(access uint32) (*mgr.Service, func(), error) {
	scm, err := windows.OpenSCManager(nil, nil, windows.SC_MANAGER_CONNECT)
	if err != nil {
		return nil, nil, err
	}
	name, _ := windows.UTF16PtrFromString(ServiceName)
	h, err := windows.OpenService(scm, name, access)
	if err != nil {
		windows.CloseServiceHandle(scm)
		return nil, nil, err
	}
	return &mgr.Service{Name: ServiceName, Handle: h}, func() {
		windows.CloseServiceHandle(h)
		windows.CloseServiceHandle(scm)
	}, nil
}

const queryAccess = windows.SERVICE_QUERY_CONFIG | windows.SERVICE_QUERY_STATUS

func Status(home string) (string, error) {
	s, done, err := openService(queryAccess)
	if err == windows.ERROR_SERVICE_DOES_NOT_EXIST {
		return "not installed", nil
	}
	if err != nil {
		return "unknown (" + err.Error() + ")", nil
	}
	defer done()
	if cfg, err := s.Config(); err == nil && !strings.Contains(strings.ToLower(cfg.BinaryPathName), strings.ToLower(home)) {
		return "not installed for this home (the service serves another --home)", nil
	}
	st, err := s.Query()
	if err != nil {
		return "installed", nil
	}
	names := map[svc.State]string{svc.Stopped: "stopped", svc.StartPending: "starting", svc.StopPending: "stopping",
		svc.Running: "running", svc.Paused: "paused"}
	return "installed, " + names[st.State], nil
}

func IsService() bool {
	ok, _ := svc.IsWindowsService()
	return ok
}

type handler struct {
	run func(ctx context.Context) error
}

func (h handler) Execute(args []string, r <-chan svc.ChangeRequest, s chan<- svc.Status) (bool, uint32) {
	s <- svc.Status{State: svc.StartPending}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- h.run(ctx) }()
	s <- svc.Status{State: svc.Running, Accepts: svc.AcceptStop | svc.AcceptShutdown}
	for {
		select {
		case err := <-done:
			cancel()
			if err != nil {
				return true, 1
			}
			return false, 0
		case c := <-r:
			switch c.Cmd {
			case svc.Interrogate:
				s <- c.CurrentStatus
			case svc.Stop, svc.Shutdown:
				s <- svc.Status{State: svc.StopPending}
				cancel()
				<-done
				return false, 0
			}
		}
	}
}

// RunService runs under the Service Control Manager; Stop is a graceful
// shutdown (tasks marked INTERRUPTED, §4).
func RunService(run func(ctx context.Context) error) error {
	return svc.Run(ServiceName, handler{run: run})
}

// --- LSA: grant SeServiceLogonRight ---

type lsaUnicodeString struct {
	Length        uint16
	MaximumLength uint16
	Buffer        *uint16
}

type lsaObjectAttributes struct {
	Length                   uint32
	RootDirectory            windows.Handle
	ObjectName               *lsaUnicodeString
	Attributes               uint32
	SecurityDescriptor       uintptr
	SecurityQualityOfService uintptr
}

var (
	advapi32                = windows.NewLazySystemDLL("advapi32.dll")
	procLsaOpenPolicy       = advapi32.NewProc("LsaOpenPolicy")
	procLsaAddAccountRights = advapi32.NewProc("LsaAddAccountRights")
	procLsaClose            = advapi32.NewProc("LsaClose")
	procLsaNtStatusToWinErr = advapi32.NewProc("LsaNtStatusToWinError")
)

const (
	policyCreateAccount = 0x00000010
	policyLookupNames   = 0x00000800
)

func ntErr(status uintptr) error {
	if status == 0 {
		return nil
	}
	r, _, _ := procLsaNtStatusToWinErr.Call(status)
	return windows.Errno(r)
}

func grantServiceLogonRight(sidStr string) error {
	sid, err := windows.StringToSid(sidStr)
	if err != nil {
		return err
	}
	var attrs lsaObjectAttributes
	attrs.Length = uint32(unsafe.Sizeof(attrs))
	var policy windows.Handle
	r, _, _ := procLsaOpenPolicy.Call(0, uintptr(unsafe.Pointer(&attrs)), policyCreateAccount|policyLookupNames, uintptr(unsafe.Pointer(&policy)))
	if err := ntErr(r); err != nil {
		return fmt.Errorf("LsaOpenPolicy: %w", err)
	}
	defer procLsaClose.Call(uintptr(policy))
	right, _ := windows.UTF16FromString("SeServiceLogonRight")
	us := lsaUnicodeString{Length: uint16((len(right) - 1) * 2), MaximumLength: uint16(len(right) * 2), Buffer: &right[0]}
	r, _, _ = procLsaAddAccountRights.Call(uintptr(policy), uintptr(unsafe.Pointer(sid)), uintptr(unsafe.Pointer(&us)), 1)
	if err := ntErr(r); err != nil {
		return fmt.Errorf("LsaAddAccountRights: %w", err)
	}
	return nil
}

// Installed reports whether the kith service exists and serves this home.
func Installed(home string) bool {
	s, done, err := openService(queryAccess)
	if err != nil {
		return false
	}
	defer done()
	cfg, err := s.Config()
	return err == nil && strings.Contains(strings.ToLower(cfg.BinaryPathName), strings.ToLower(home))
}

func withService(fn func(*mgr.Service) error) error {
	s, done, err := openService(queryAccess | windows.SERVICE_START | windows.SERVICE_STOP)
	if err == windows.ERROR_ACCESS_DENIED {
		return fmt.Errorf("controlling the kith service needs an elevated (Administrator) terminal")
	}
	if err != nil {
		return err
	}
	defer done()
	return fn(s)
}

// Start starts the service (needs rights to control it; elevated by default).
func Start(home string) error {
	return withService(func(s *mgr.Service) error {
		if err := s.Start(); err != nil {
			return fmt.Errorf("start service: %w (run in an elevated terminal)", err)
		}
		return nil
	})
}

// Stop stops the service and waits up to 20 s for it to stop.
func Stop(home string) error {
	return withService(func(s *mgr.Service) error {
		st, err := s.Control(svc.Stop)
		if err != nil {
			return fmt.Errorf("stop service: %w (run in an elevated terminal)", err)
		}
		deadline := time.Now().Add(20 * time.Second)
		for st.State != svc.Stopped && time.Now().Before(deadline) {
			time.Sleep(300 * time.Millisecond)
			if st, err = s.Query(); err != nil {
				return err
			}
		}
		return nil
	})
}
