package main

import (
	"embed"
	"encoding/json"
	"fmt"
	"runtime"
	"sync"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/services/notifications"
)

//go:embed icons/*.png
var iconFS embed.FS

func icon(name string) []byte {
	b, _ := iconFS.ReadFile("icons/" + name)
	return b
}

// appSink publishes poll results to the window, tray and notifications.
type appSink struct {
	app   *application.App
	tray  *application.SystemTray
	notif *notifications.NotificationService

	status, approvals, active *application.MenuItem
	daemon                    *application.MenuItem
	menu                      *application.Menu

	mu      sync.Mutex
	lastKey string
	running bool
}

const approvalCategory = "kith-approval"

func newAppSink(app *application.App, notif *notifications.NotificationService, svc *KithService, flyout *application.WebviewWindow) *appSink {
	s := &appSink{app: app, notif: notif}
	m := application.NewMenu()
	s.status = m.Add("Kith: starting…").SetEnabled(false)
	s.active = m.Add("").SetEnabled(false).SetHidden(true)
	s.approvals = m.Add("").SetHidden(true).OnClick(func(*application.Context) { showWindow(app, "#/approvals") })
	m.AddSeparator()
	m.Add("Open Kith").OnClick(func(*application.Context) { showWindow(app, "") })
	s.daemon = m.Add("Start daemon").OnClick(func(*application.Context) {
		s.mu.Lock()
		running := s.running
		s.mu.Unlock()
		action := "start"
		if running {
			action = "stop"
		}
		go func() {
			if _, err := svc.Daemon(action); err != nil {
				s.Notify(Notice{Kind: "error", Title: "Kith daemon " + action + " failed", Body: err.Error()})
			}
		}()
	})
	m.AddSeparator()
	m.Add("Quit Kith").OnClick(func(*application.Context) { app.Quit() })
	s.menu = m

	t := app.SystemTray.New()
	if runtime.GOOS == "darwin" {
		t.SetTemplateIcon(icon("tray-template.png"))
	} else {
		t.SetIcon(icon("tray-off.png"))
	}
	t.SetTooltip("Kith")
	t.SetMenu(m)
	if flyout != nil {
		// Left click toggles the flyout; right click opens the menu.
		t.AttachWindow(flyout).WindowOffset(12).WindowDebounce(200 * time.Millisecond)
	} else {
		t.OnClick(func() { showWindow(app, "") })
	}
	s.tray = t

	notif.RegisterNotificationCategory(notifications.NotificationCategory{
		ID: approvalCategory,
		Actions: []notifications.NotificationAction{
			{ID: "approve", Title: "Approve"},
			{ID: "deny", Title: "Deny", Destructive: true},
		},
	})
	return s
}

// showWindow brings the main window forward, optionally at a route.
func showWindow(app *application.App, route string) {
	w, ok := app.Window.Get("main")
	if !ok {
		return
	}
	w.Show()
	w.Focus()
	if route != "" {
		w.ExecJS(fmt.Sprintf("window.location.hash = %q", route))
	}
}

func (s *appSink) State(snap Snapshot) { s.app.Event.Emit("kith:state", snap) }

func (s *appSink) Tray(snap Snapshot, peers []peerLite) {
	online := 0
	for _, p := range peers {
		if p.Reachable {
			online++
		}
	}
	status := "Kith: daemon not running"
	if snap.Running {
		var st statusLite
		json.Unmarshal(snap.Status, &st)
		status = fmt.Sprintf("Kith: running · %d/%d peers online", online, len(peers))
	}
	key := fmt.Sprintf("%s|%d|%d|%v", status, snap.Pending, snap.Active, snap.Running)
	s.mu.Lock()
	s.running = snap.Running
	changed := key != s.lastKey
	s.lastKey = key
	s.mu.Unlock()
	if !changed {
		return
	}
	s.status.SetLabel(status)
	s.approvals.SetHidden(snap.Pending == 0).SetLabel(fmt.Sprintf("%d task(s) waiting for approval…", snap.Pending))
	s.active.SetHidden(snap.Active == 0).SetLabel(fmt.Sprintf("%d active task(s)", snap.Active))
	if snap.Running {
		s.daemon.SetLabel("Stop daemon")
	} else {
		s.daemon.SetLabel("Start daemon")
	}
	s.menu.Update()
	tip := status
	if snap.Pending > 0 {
		tip += fmt.Sprintf(" · %d awaiting approval", snap.Pending)
	}
	s.tray.SetTooltip(tip)
	switch runtime.GOOS {
	case "darwin":
		if snap.Pending > 0 {
			s.tray.SetTemplateIcon(icon("tray-template-pending.png"))
			s.tray.SetLabel(fmt.Sprint(snap.Pending))
		} else {
			s.tray.SetTemplateIcon(icon("tray-template.png"))
			s.tray.SetLabel("")
		}
	default:
		switch {
		case !snap.Running:
			s.tray.SetIcon(icon("tray-off.png"))
		case snap.Pending > 0:
			s.tray.SetIcon(icon("tray-pending.png"))
		default:
			s.tray.SetIcon(icon("tray.png"))
		}
	}
}

func (s *appSink) Notify(n Notice) {
	opts := notifications.NotificationOptions{ID: n.Kind + ":" + n.TaskID, Title: n.Title, Body: n.Body,
		Data: map[string]interface{}{"task_id": n.TaskID, "kind": n.Kind}}
	if n.Kind == "approval" {
		opts.CategoryID = approvalCategory
		if err := s.notif.SendNotificationWithActions(opts); err == nil {
			return
		}
	}
	s.notif.SendNotification(opts)
}

// handleNotification routes a click / action button.
func handleNotification(app *application.App, svc *KithService, r notifications.NotificationResult) {
	if r.Error != nil {
		return
	}
	id, _ := r.Response.UserInfo["task_id"].(string)
	switch r.Response.ActionIdentifier {
	case "approve":
		if id != "" {
			go svc.Approve(id)
		}
	case "deny":
		if id != "" {
			go svc.Deny(id)
		}
	default:
		route := ""
		if id != "" {
			route = "#/tasks/" + id
		}
		if kind, _ := r.Response.UserInfo["kind"].(string); kind == "approval" {
			route = "#/approvals"
		}
		showWindow(app, route)
	}
}
