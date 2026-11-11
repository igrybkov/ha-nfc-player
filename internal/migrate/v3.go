package migrate

import "github.com/igrybkov/ha-nfc-player/internal/store"

// v3to4 adds an optional "name" to record items. Old items have none, so
// nothing changes; the version moves anyway, so an app of version 3, which
// would drop the names it does not know on its next save, refuses to write
// these files.
func v3to4(*store.Tree) error { return nil }
