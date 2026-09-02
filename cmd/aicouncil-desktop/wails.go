package main

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/runtime"
)

func runWails(app *DesktopApp) error {
	return wails.Run(&options.App{
		Title:     "AI Council",
		Width:     1280,
		Height:    860,
		MinWidth:  1024,
		MinHeight: 720,
		AssetServer: &assetserver.Options{
			Assets: desktopAssets,
		},
		OnStartup: func(ctx context.Context) {
			app.setWailsContext(ctx)
		},
		OnDomReady: func(ctx context.Context) {
			if _, err := app.Start(); err != nil {
				runtime.LogError(ctx, fmt.Sprintf("start desktop services: %v", err))
				return
			}
			connection, err := app.WebRuntime()
			if err != nil {
				runtime.LogError(ctx, fmt.Sprintf("get desktop runtime: %v", err))
				return
			}
			payload, err := json.Marshal(connection)
			if err != nil {
				runtime.LogError(ctx, "serialize desktop runtime")
				return
			}
			runtime.WindowExecJS(ctx, "window.__AI_COUNCIL_DESKTOP__="+string(payload)+";")
		},
		OnShutdown: func(context.Context) {
			_ = app.Stop()
		},
		Bind: []interface{}{app},
	})
}

// ChooseWorkspace opens the native directory chooser and validates the result
// before it becomes the Runner workspace.
func (a *DesktopApp) ChooseWorkspace() (string, error) {
	ctx := a.wailsContext()
	if ctx == nil {
		return "", fmt.Errorf("desktop window is not ready")
	}
	path, err := runtime.OpenDirectoryDialog(ctx, runtime.OpenDialogOptions{Title: "选择 AI Council 工作区"})
	if err != nil {
		return "", err
	}
	if path == "" {
		return "", nil
	}
	if err := a.OpenWorkspace(path); err != nil {
		return "", err
	}
	return path, nil
}
