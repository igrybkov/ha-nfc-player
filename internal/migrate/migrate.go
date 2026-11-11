// Package migrate owns the schema version of the files in config_dir and
// the steps that bring older files up to it.
//
// Every file holds the version it was written at. Reading migrates all of
// them in memory, one step per version, so files from any older version
// load; nothing is written until the user changes something (see
// store.Set). To change the schema:
//
//  1. Add a step to Steps that takes version Current to Current+1. It works
//     on YAML nodes only: the app types will move on, the step must not.
//  2. Bump Current, and add any new file name to Files. Never remove a
//     name: a file of an old version must still be found.
//  3. Add testdata/v<new Current>/ with files at the new version.
package migrate

import (
	"log/slog"

	"github.com/igrybkov/ha-nfc-player/internal/store"
)

// Current is the schema version this build reads and writes.
const Current = 3

// Files are the names of every file in config_dir, of every version.
var Files = []string{
	"tags.yaml",  // v1
	"pools.yaml", // v1
	"records.yaml",
	"settings.yaml",
	"templates.yaml",
	"backgrounds.yaml",
}

// Steps takes version i+1 to i+2 at index i.
var Steps = []store.Step{v1to2, v2to3}

// Open returns the set of files in dir.
func Open(dir string, log *slog.Logger) *store.Set {
	return store.OpenSet(dir, store.SetOptions{Current: Current, Files: Files, Steps: Steps, Logger: log})
}
