// Command nfc-player is the NFC Player Home Assistant app.
//
// Owner: phase 2 (docs/PLAN.md section 10). Wiring (config, storage, HA
// client, players, web) lands there.
package main

import (
	"flag"
	"fmt"
	"log/slog"
	"os"
)

// version is set at build time with -ldflags "-X main.version=...".
var version = "dev"

func main() {
	showVersion := flag.Bool("version", false, "print the version and exit")
	flag.Parse()
	if *showVersion {
		fmt.Println(version)
		return
	}
	slog.Info("nfc-player", "version", version)
	slog.Error("not wired yet (phase 2)")
	os.Exit(1)
}
