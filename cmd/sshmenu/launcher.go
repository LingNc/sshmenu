package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// launcherPath returns the path to the launchers config file.
func launcherPath() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, "sshmenu", "launchers")
}

// parseLaunchers reads the launchers config file and returns a slice of
// Launcher. Returns nil (not an error) if the file does not exist.
// Each non-empty, non-comment line is parsed as "name=command".
func parseLaunchers() ([]Launcher, error) {
	p := launcherPath()
	if p == "" {
		return nil, nil
	}
	data, err := os.ReadFile(p)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("cannot read launchers config: %w", err)
	}
	var result []Launcher
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			continue
		}
		name := strings.TrimSpace(parts[0])
		cmd := strings.TrimSpace(parts[1])
		if name == "" || cmd == "" {
			continue
		}
		result = append(result, Launcher{Name: name, Command: cmd})
	}
	return result, nil
}

// connectLauncher launches a custom command with full terminal I/O passthrough.
func connectLauncher(l Launcher) error {
	fields := strings.Fields(l.Command)
	if len(fields) == 0 {
		return fmt.Errorf("empty command for launcher %q", l.Name)
	}
	cmd := exec.Command(fields[0], fields[1:]...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}
