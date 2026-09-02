package main

import "embed"

// desktopAssets are replaced with the static Next.js export by `pnpm
// desktop:build` before Wails produces a release binary.
//
//go:embed all:frontend/dist
var desktopAssets embed.FS
