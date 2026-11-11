// Package ui embeds the static UI (HTML, CSS, vanilla JS modules).
package ui

import "embed"

// FS holds the UI files. Use relative URLs only (Ingress).
//
//go:embed index.html *.css *.js
var FS embed.FS
