package main

import (
	"embed"
	"log"
	"os"
	"strings"

	"github.com/wailsapp/wails/v3/pkg/application"
)

//go:embed all:frontend/dist
var assets embed.FS

//go:embed icon.png
var iconData []byte

func main() {
	openURL := ""
	for _, arg := range os.Args[1:] {
		if strings.HasPrefix(arg, "--open=") {
			openURL = strings.TrimPrefix(arg, "--open=")
		} else if arg == "--open" {
			continue
		} else if openURL == "" && !strings.HasPrefix(arg, "--") {
			openURL = arg
		}
	}

	if openURL != "" && SendToExistingInstance(openURL) {
		return
	}

	startURL := "/"
	if openURL != "" {
		startURL = "/?open=" + openURL
	}

	app := application.New(application.Options{
		Name:        "WebDesk",
		Description: "把网站当应用用的桌面壳",
		Services: []application.Service{
			application.NewService(NewSiteService()),
		},
		Assets: application.AssetOptions{
			Handler: application.BundledAssetFileServer(assets),
		},
		Mac: application.MacOptions{
			ApplicationShouldTerminateAfterLastWindowClosed: true,
		},
	})

	app.Window.NewWithOptions(application.WebviewWindowOptions{
		Title:            "WebDesk",
		Width:            1280,
		Height:           800,
		MinWidth:         900,
		MinHeight:        600,
		BackgroundColour: application.NewRGB(30, 30, 46),
		URL:              startURL,
		Linux: application.LinuxWindow{
			Icon: iconData,
		},
	})

	if err := app.Run(); err != nil {
		log.Fatal(err)
	}
	_ = os.Stdout
}
