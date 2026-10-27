package assets

import "embed"

// Files contains the dashboard and installer served by the panel.
//
//go:embed web/* scripts/*
var Files embed.FS
