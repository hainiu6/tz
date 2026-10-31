package downloads

import "embed"

// Files contains the Linux agents bundled into each panel release.
// The agents do not import this package, so building them cannot embed old agents.
//
//go:embed agents/*
var Files embed.FS
