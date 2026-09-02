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
