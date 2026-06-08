package main

import (
	"os"
	"path/filepath"
	"strings"
)

// historyPath returns the path to the file that stores the last selected
// host alias. The directory is created on first write.
func historyPath() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, "sshmenu", "last")
}

// loadLastHost reads the last selected host alias from the config file.
// Returns an empty string if the file does not exist or cannot be read.
func loadLastHost() string {
	p := historyPath()
	if p == "" {
		return ""
	}
	data, err := os.ReadFile(p)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

// saveLastHost writes the given alias to the config file, creating
// parent directories as needed. Errors are silently ignored -- saving
// is best-effort and must not block the SSH connection.
func saveLastHost(alias string) {
	p := historyPath()
	if p == "" {
		return
	}
	_ = os.MkdirAll(filepath.Dir(p), 0o755)
	_ = os.WriteFile(p, []byte(alias+"\n"), 0o644)
}

// reorderHosts moves the host matching alias to the front of the slice,
// preserving the relative order of all other hosts. If alias is not
// found, the original slice is returned unchanged.
func reorderHosts(hosts []SSHHost, alias string) []SSHHost {
	for i, h := range hosts {
		if h.Alias == alias {
			result := make([]SSHHost, 0, len(hosts))
			result = append(result, hosts[i])
			result = append(result, hosts[:i]...)
			result = append(result, hosts[i+1:]...)
			return result
		}
	}
	return hosts
}
