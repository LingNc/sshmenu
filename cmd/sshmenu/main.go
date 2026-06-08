// sshmenu is a cross-platform terminal UI for selecting and connecting to
// SSH hosts defined in ~/.ssh/config.
//
// Usage: sshmenu
//
// Keys (inside the TUI):
//   - j / Down        move cursor down
//   - k / Up          move cursor up
//   - (type)          filter the list (substring, case-insensitive)
//   - Backspace       delete last filter character
//   - Esc             clear filter (or quit if filter is empty)
//   - Enter           connect to selected host (exits TUI, runs `ssh`)
//   - q / Ctrl+C      quit without connecting
package main

import (
	"fmt"
	"os"
)

// version is set at build time via -ldflags "-X main.version=..."
var version = "dev"

func main() {
	if len(os.Args) > 1 && (os.Args[1] == "--version" || os.Args[1] == "-v") {
		fmt.Printf("sshmenu %s\n", version)
		return
	}

	hosts, err := parseSSHConfig()
	if err != nil {
		fmt.Fprintf(os.Stderr, "sshmenu: %v\n", err)
		os.Exit(1)
	}
	if len(hosts) == 0 {
		fmt.Println("sshmenu: no hosts found in ~/.ssh/config")
		os.Exit(0)
	}

	if lastAlias := loadLastHost(); lastAlias != "" {
		hosts = reorderHosts(hosts, lastAlias)
	}

	host, err := runTUI(hosts)
	if err != nil {
		fmt.Fprintf(os.Stderr, "sshmenu: %v\n", err)
		os.Exit(1)
	}
	if host == nil {
		// User quit without picking a host.
		return
	}
	if err := connectSSH(*host); err != nil {
		fmt.Fprintf(os.Stderr, "sshmenu: %v\n", err)
		os.Exit(1)
	}
}
