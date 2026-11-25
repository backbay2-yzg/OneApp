package main

import (
	"context"
	"embed"
	"log"
	"os"
	"strings"

	"github.com/wailsapp/wails/v3/pkg/application"
	"oneapp/internal/app"
	"oneapp/internal/config"
	"oneapp/internal/domain"
	"oneapp/internal/platform/factory"
	"oneapp/internal/service"
	"oneapp/internal/storage"
)

func hasSilentFlag(args []string) bool {
	for _, arg := range args {
		if strings.EqualFold(strings.TrimSpace(arg), "--silent") {
			return true
		}
	}
	return false
}

//go:embed all:frontend/dist
var assets embed.FS

//go:embed assets/icon.png
var appIcon []byte

func main() {
	paths, err := config.ResolvePaths("")
	if err != nil {
		log.Fatalf("resolve portable paths: %v", err)
	}
	settingsStore, err := config.NewSettingsStore(paths.SettingsPath)
	if err != nil {
		log.Fatalf("load settings: %v", err)
	}
	if err := paths.EnsureDirs(settingsStore.Get()); err != nil {
		log.Fatalf("ensure directories: %v", err)
	}

	db, err := storage.OpenDB(paths.DBPath)
	if err != nil {
		log.Fatalf("open sqlite db: %v", err)
	}
	defer db.Close()

	repo := storage.NewRepo(db)
	osBridge := factory.NewOSBridge()

	searchSvc := service.NewSearchService()

	var appSvc *service.AppService
	var ctrl *app.Controller
	var launchSvc *service.LaunchService

	ingestSvc := service.NewIngestService(paths, settingsStore, repo, osBridge, func() {
		if appSvc != nil {
			appSvc.RefreshIndex()
		}
	})

	launchSvc = service.NewLaunchService(
		paths,
		settingsStore,
		repo,
		osBridge,
		func() {
			if appSvc != nil {
				appSvc.RefreshIndex()
			}
		},
		func(runningMap map[string][]int) {
			searchSvc.UpdateRunning(runningMap)
			if ctrl != nil {
				ctrl.EmitEvent("process:status", launchSvc.GetRunningStates())
			}
		},
	)

	appSvc = service.NewAppService(paths, settingsStore, repo, osBridge, ingestSvc, searchSvc, launchSvc)
	settingsSvc := service.NewSettingsService(settingsStore, osBridge)
	ctrl = app.NewController(settingsStore, repo, ingestSvc, launchSvc, appSvc)
	launchSvc.SetLogCallbacks(
		func(snap domain.AppLogSnapshot) {
			if ctrl != nil {
				ctrl.EmitEvent("app:log", snap)
			}
		},
		func(msg string) {
			if ctrl != nil {
				ctrl.EmitEvent("app:error", msg)
			}
		},
	)

	service.AttachUIHosts(appSvc, settingsSvc, ctrl)

	winSvc := app.NewWindowService(ctrl)
	iconSrv := service.NewIconServer(paths.IconsDir)

	// Hydrate in-memory search index at startup before windows open.
	_, _ = appSvc.ListApps()

	wailsApp := application.New(application.Options{
		Name:        "OneApp",
		Description: "Portable software library and instant launcher",
		Icon:        appIcon,
		Services: []application.Service{
			application.NewService(appSvc),
			application.NewService(settingsSvc),
			application.NewService(winSvc),
			application.NewServiceWithOptions(iconSrv, application.ServiceOptions{
				Name:  "IconServer",
				Route: "/icons",
			}),
		},
		Assets: application.AssetOptions{
			Handler: application.AssetFileServerFS(assets),
		},
		SingleInstance: &application.SingleInstanceOptions{
			UniqueID: "com.oneapp.desktop.portable",
			OnSecondInstanceLaunch: func(_ application.SecondInstanceData) {
				ctrl.ShowMain()
			},
		},
	})

	silentStart := hasSilentFlag(os.Args[1:])
	ctrl.AttachApp(wailsApp, appIcon, silentStart)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go launchSvc.StartMonitor(ctx)

	if err := wailsApp.Run(); err != nil {
		log.Fatal(err)
	}
}
