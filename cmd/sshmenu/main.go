// sshmenu is a cross-platform terminal UI for selecting and connecting to
// SSH hosts defined in ~/.ssh/config, and for running user-defined
// launcher commands.
//
// Usage: sshmenu
//
// Keys (inside the TUI):
//   - j / Down        move cursor down
//   - k / Up          move cursor up
//   - (type)          filter the list (substring, case-insensitive)
//   - Backspace       delete last filter character
//   - Esc             clear filter (or quit if filter is empty)
//   - Enter           connect / launch selected item (exits TUI)
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

	// Parse SSH hosts.
	hosts, err := parseSSHConfig()
	if err != nil {
		fmt.Fprintf(os.Stderr, "sshmenu: %v\n", err)
		os.Exit(1)
	}

	// Parse launchers. A missing launchers file is not an error.
	launchers, err := parseLaunchers()
	if err != nil {
		fmt.Fprintf(os.Stderr, "sshmenu: %v\n", err)
		os.Exit(1)
	}

	// Merge into a single ordered list. SSH hosts come first, then
	// launchers; the LRU reorder pass may rearrange them.
	items := make([]ListItem, 0, len(hosts)+len(launchers))
	for _, h := range hosts {
		items = append(items, ListItem{Kind: itemSSH, Alias: h.Alias, Host: h})
	}
	for _, l := range launchers {
		items = append(items, ListItem{Kind: itemLauncher, Alias: l.Name, Launcher: l})
	}

	if len(items) == 0 {
		fmt.Println("sshmenu: no hosts or launchers found")
		os.Exit(0)
	}

	history := loadHistory()
	items = reorderItems(items, history)

	item, err := runTUI(items, history)
	if err != nil {
		fmt.Fprintf(os.Stderr, "sshmenu: %v\n", err)
		os.Exit(1)
	}
	if item == nil {
		// User quit without picking an item.
		return
	}

	switch item.Kind {
	case itemSSH:
		if err := connectSSH(item.Host); err != nil {
			fmt.Fprintf(os.Stderr, "sshmenu: %v\n", err)
			os.Exit(1)
		}
	case itemLauncher:
		if err := connectLauncher(item.Launcher); err != nil {
			fmt.Fprintf(os.Stderr, "sshmenu: %v\n", err)
			os.Exit(1)
		}
	}
}
