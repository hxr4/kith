// Command kith-gui is the Kith desktop app: a tray icon, approval
// notifications and a window over the local kith daemon. It is a thin IPC
// client, exactly like the kith CLI.
package main

import (
	"context"
	"embed"
	"log"
	"os"
	"runtime"
	"slices"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
	"github.com/wailsapp/wails/v3/pkg/services/notifications"
)

//go:embed all:frontend/dist
var assets embed.FS

func main() {
	lite := isLite()
	startHidden := slices.Contains(os.Args[1:], "--hidden")
	svc := NewKithService()
	notif := notifications.New()

	app := application.New(application.Options{
		Name:        "Kith",
		Description: "Delegate work between your paired machines",
		Services: []application.Service{
			application.NewService(svc),
			application.NewService(notif),
		},
		Assets: application.AssetOptions{Handler: application.AssetFileServerFS(assets)},
		SingleInstance: &application.SingleInstanceOptions{
			UniqueID:               "dev.kith.gui",
			OnSecondInstanceLaunch: func(application.SecondInstanceData) {},
		},
		Mac: application.MacOptions{
			// Menu-bar app: no Dock icon; the window opens from the tray.
			// KITH_GUI_DOCK=1 shows a Dock icon instead.
			ActivationPolicy: macPolicy(),
		},
		Linux: application.LinuxOptions{DisableQuitOnLastWindowClosed: true, ProgramName: "kith-gui"},
	})
	svc.app = app

	macBackdrop := application.MacBackdropTranslucent
	if lite {
		macBackdrop = application.MacBackdropNormal
	}
	// Windows: frameless with the app's own Fluent title bar (caption
	// buttons are native non-client regions, so Snap layouts work), Mica
	// behind the title bar and navigation on Windows 11 22H2+.
	winChrome := chrome() == "windows"
	opts := application.WebviewWindowOptions{
		Name:             "main",
		Title:            "Kith",
		Width:            1120,
		Height:           740,
		MinWidth:         900,
		MinHeight:        580,
		Hidden:           startHidden,
		URL:              "/",
		Frameless:        winChrome,
		EnableFileDrop:   true,
		BackgroundColour: application.NewRGB(32, 32, 32),
		Mac: application.MacWindow{
			InvisibleTitleBarHeight: 44,
			Backdrop:                macBackdrop,
			TitleBar:                application.MacTitleBarHiddenInset,
		},
		Windows: application.WindowsWindow{BackdropType: application.Mica, NonClientRegionSupport: true},
	}
	if lite {
		opts.Width, opts.Height, opts.MinWidth, opts.MinHeight = 800, 480, 640, 400
	}
	if winChrome && micaSupported() {
		opts.BackgroundType = application.BackgroundTypeTranslucent
		opts.BackgroundColour = application.NewRGBA(0, 0, 0, 0)
	}
	win := app.Window.NewWithOptions(opts)
	// Files dropped on the window open New task.
	win.OnWindowEvent(events.Common.WindowFilesDropped, func(e *application.WindowEvent) {
		app.Event.Emit("kith:drop", e.Context().DroppedFiles())
	})

	// The tray flyout (Windows and Linux): a small window attached to the
	// tray icon, hidden when it loses focus.
	var flyout *application.WebviewWindow
	if runtime.GOOS != "darwin" || os.Getenv("KITH_GUI_CHROME") != "" { // KITH_GUI_CHROME: preview on macOS
		fo := application.WebviewWindowOptions{
			Name:             "flyout",
			Title:            "Kith",
			Width:            360,
			Height:           560,
			Frameless:        true,
			DisableResize:    true,
			AlwaysOnTop:      true,
			Hidden:           true,
			URL:              "/#/flyout",
			BackgroundColour: application.NewRGB(32, 32, 32),
			Windows:          application.WindowsWindow{BackdropType: application.Acrylic, HiddenOnTaskbar: true},
		}
		if winChrome && micaSupported() {
			fo.BackgroundType = application.BackgroundTypeTranslucent
			fo.BackgroundColour = application.NewRGBA(0, 0, 0, 0)
		}
		flyout = app.Window.NewWithOptions(fo)
	}
	// Closing the window keeps Kith in the tray; quit from the tray menu.
	win.RegisterHook(events.Common.WindowClosing, func(e *application.WindowEvent) {
		e.Cancel()
		win.Hide()
	})

	sink := newAppSink(app, notif, svc, flyout)
	poller := NewPoller(svc.cl, sink, lite)
	svc.poller = poller
	notif.OnNotificationResponse(func(r notifications.NotificationResult) { handleNotification(app, svc, r) })

	ctx, cancel := context.WithCancel(context.Background())
	app.Event.OnApplicationEvent(events.Common.ApplicationStarted, func(*application.ApplicationEvent) {
		if runtime.GOOS == "darwin" {
			if ok, _ := notif.CheckNotificationAuthorization(); !ok {
				notif.RequestNotificationAuthorization()
			}
		}
		go poller.Run(ctx)
	})
	err := app.Run()
	cancel()
	if err != nil {
		log.Fatal(err)
	}
}

func macPolicy() application.ActivationPolicy {
	if os.Getenv("KITH_GUI_DOCK") == "1" {
		return application.ActivationPolicyRegular
	}
	return application.ActivationPolicyAccessory
}
