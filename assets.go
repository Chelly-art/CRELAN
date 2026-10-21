package assets

import "embed"

// Web contains the static dashboard served by the API.
//
//go:embed web/*
var Web embed.FS
