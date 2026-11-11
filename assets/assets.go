// Package assets embeds static assets: Noto Sans Regular and Bold (OFL, see
// fonts/OFL.txt), with Cyrillic coverage.
package assets

import "embed"

// Fonts holds fonts/NotoSans-Regular.ttf and fonts/NotoSans-Bold.ttf.
//
//go:embed fonts/*.ttf
var Fonts embed.FS
