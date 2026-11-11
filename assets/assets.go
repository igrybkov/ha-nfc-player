// Package assets embeds static assets: Noto Sans Regular and Bold (OFL, see
// fonts/OFL.txt), with Cyrillic coverage, and the made-up album covers that
// theme previews use (samples/gen.go draws them).
package assets

import "embed"

// Fonts holds fonts/NotoSans-Regular.ttf and fonts/NotoSans-Bold.ttf.
//
//go:embed fonts/*.ttf
var Fonts embed.FS

// Samples holds samples/pale.jpg, samples/dark.jpg and samples/busy.jpg.
//
//go:embed samples/*.jpg
var Samples embed.FS
