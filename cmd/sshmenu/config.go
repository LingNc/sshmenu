package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// parseSSHConfig reads ~/.ssh/config and returns a slice of hosts.
//
// Returns an empty slice (not an error) if the file does not exist -- this
// lets main() print a friendly message and exit 0.
//
// SSH config rules implemented (matching OpenSSH):
//   - "Host" line starts a block; patterns are space-separated aliases.
//   - Indented key-value lines are properties of the current block.
//   - Keywords are case-insensitive (stored lowercase).
//   - First occurrence of a key wins within a block (and across global+block).
//   - Trailing unquoted "#" starts a comment, even on a value line.
//   - Surrounding double quotes are stripped from values.
//   - "Host *" stores its properties as global defaults; the wildcard
//     itself never appears in the returned hosts.
func parseSSHConfig() ([]SSHHost, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("cannot determine home directory: %w", err)
	}

	data, err := os.ReadFile(filepath.Join(home, ".ssh", "config"))
	if os.IsNotExist(err) {
		return []SSHHost{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("cannot read SSH config: %w", err)
	}

	lines := strings.Split(string(data), "\n")

	// globalDefaults: properties from "Host *" -- applied to all later hosts.
	// currentHosts:   aliases being accumulated in the current block.
	// currentProps:   properties for the current block (first-value-wins).
	var (
		globalDefaults = map[string]string{}
		hosts          []SSHHost
		currentHosts   []string
		currentProps   = map[string]string{}
	)

	flush := func() {
		if len(currentHosts) == 0 {
			return
		}
		for _, alias := range currentHosts {
			if alias == "*" {
				// Promote the block's properties into globalDefaults.
				// First-value-wins: don't overwrite an already-set default.
				for k, v := range currentProps {
					if _, exists := globalDefaults[k]; !exists {
						globalDefaults[k] = v
					}
				}
				continue
			}
			h := SSHHost{Alias: alias}
			// Block-specific values win over Host * defaults. Apply the
			// block's own properties first, then fill in any still-empty
			// fields from globalDefaults.
			applyProps(&h, currentProps)
			applyProps(&h, globalDefaults)
			hosts = append(hosts, h)
		}
		currentHosts = nil
		currentProps = map[string]string{}
	}

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}

		// Host directive -- flush previous block and start a new one.
		if strings.HasPrefix(strings.ToLower(trimmed), "host ") {
			flush()
			fields := strings.Fields(trimmed)
			currentHosts = fields[1:]
			currentProps = map[string]string{}
			continue
		}

		// Indented key-value line: belongs to the current block.
		if line[0] == ' ' || line[0] == '\t' {
			key, value := parseKeyValue(trimmed)
			if key == "" {
				continue
			}
			// First-value-wins within a block.
			if _, exists := currentProps[key]; !exists {
				currentProps[key] = value
			}
		}
	}

	flush()
	// Post-process: apply globalDefaults to any host that still has empty
	// fields. This makes "Host *" position-independent -- per OpenSSH, its
	// properties apply to every host that hasn't had them set explicitly,
	// regardless of where the "Host *" block appears in the file.
	for i := range hosts {
		applyProps(&hosts[i], globalDefaults)
	}
	return hosts, nil
}

// applyProps merges SSH config keyword -> value mappings into h, but only
// for fields that h doesn't already have set. This gives us first-value-wins
// across the global + block merge.
func applyProps(h *SSHHost, props map[string]string) {
	if h.HostName == "" {
		h.HostName = props["hostname"]
	}
	if h.User == "" {
		h.User = props["user"]
	}
	if h.Port == "" {
		h.Port = props["port"]
	}
	if h.IdentityFile == "" {
		h.IdentityFile = props["identityfile"]
	}
	if h.ProxyJump == "" {
		h.ProxyJump = props["proxyjump"]
	}
}

// parseKeyValue splits an indented line like "  HostName example.com" into
// the lowercase keyword and the (comment-stripped, quote-stripped) value.
// Returns ("", "") if the line has no whitespace separator.
func parseKeyValue(line string) (string, string) {
	parts := strings.SplitN(line, " ", 2)
	if len(parts) < 2 {
		return "", ""
	}
	key := strings.ToLower(strings.TrimSpace(parts[0]))
	value := strings.TrimSpace(parts[1])
	value = stripComment(value)
	value = stripQuotes(value)
	return key, value
}

// stripComment removes a trailing "# ..." comment, but only when the '#'
// is OUTSIDE of double quotes AND is preceded by whitespace (or is at
// position 0). This matches ssh_config(5): "no#hash" is preserved as-is,
// while "no #hash" strips the comment.
func stripComment(s string) string {
	inQuote := false
	for i := 0; i < len(s); i++ {
		if s[i] == '"' {
			inQuote = !inQuote
			continue
		}
		if s[i] == '#' && !inQuote {
			if i == 0 || s[i-1] == ' ' || s[i-1] == '\t' {
				return strings.TrimSpace(s[:i])
			}
		}
	}
	return s
}

// stripQuotes removes a single pair of surrounding double quotes.
func stripQuotes(s string) string {
	if len(s) >= 2 && s[0] == '"' && s[len(s)-1] == '"' {
		return s[1 : len(s)-1]
	}
	return s
}
