// Package deps blank-imports every module dependency so go mod tidy keeps
// them before the code that uses them lands. Remove an import once a real
// package uses that dependency.
//
// Owner: phase 0.
package deps

import (
	_ "github.com/coder/websocket"
	_ "github.com/signintech/gopdf"
	_ "go.yaml.in/yaml/v3"
	_ "golang.org/x/image/draw"
	_ "golang.org/x/image/font/sfnt"
)
