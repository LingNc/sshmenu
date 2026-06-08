package main

import (
	"os"
	"path/filepath"
	"strings"
)

const maxHistoryEntries = 20

// historyPath returns the path to the file that stores the LRU host
// history. The directory is created on first write.
func historyPath() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, "sshmenu", "last")
}

// loadHistory reads the LRU host history from the config file.
// Each line is one alias, most recent first. Returns nil if the file
// does not exist or cannot be read.
func loadHistory() []string {
	p := historyPath()
	if p == "" {
		return nil
	}
	data, err := os.ReadFile(p)
	if err != nil {
		return nil
	}
	var result []string
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		if line != "" {
			result = append(result, line)
		}
	}
	return result
}

// saveHistory writes the full LRU history to the config file, creating
// parent directories as needed. Errors are silently ignored -- saving
// is best-effort and must not block the SSH connection.
func saveHistory(history []string) {
	p := historyPath()
	if p == "" {
		return
	}
	_ = os.MkdirAll(filepath.Dir(p), 0o755)
	_ = os.WriteFile(p, []byte(strings.Join(history, "\n")+"\n"), 0o644)
}

// updateHistory moves alias to the front of the history list, removing
// any existing occurrence. The list is capped at maxHistoryEntries.
func updateHistory(history []string, alias string) []string {
	result := make([]string, 0, len(history)+1)
	result = append(result, alias)
	for _, h := range history {
		if h != alias {
			result = append(result, h)
		}
	}
	if len(result) > maxHistoryEntries {
		result = result[:maxHistoryEntries]
	}
	return result
}

// reorderHosts reorders hosts so that those appearing in the history
// come first (in LRU order), followed by hosts not in history
// (preserving their original order). This gives the user a
// most-recently-used-at-top experience.
func reorderHosts(hosts []SSHHost, history []string) []SSHHost {
	if len(history) == 0 {
		return hosts
	}
	hostMap := make(map[string]SSHHost, len(hosts))
	for _, h := range hosts {
		hostMap[h.Alias] = h
	}
	result := make([]SSHHost, 0, len(hosts))
	for _, alias := range history {
		if h, ok := hostMap[alias]; ok {
			result = append(result, h)
		}
	}
	historySet := make(map[string]bool, len(history))
	for _, alias := range history {
		historySet[alias] = true
	}
	for _, h := range hosts {
		if !historySet[h.Alias] {
			result = append(result, h)
		}
	}
	return result
}
