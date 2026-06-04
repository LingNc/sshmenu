// sshmenu is a cross-platform terminal UI for selecting and connecting to
// SSH hosts defined in ~/.ssh/config.
//
// Usage: sshmenu
//
// Keys (inside the TUI):
//   - j / Down        move cursor down
//   - k / Up          move cursor up
//   - /               start filter (type to narrow list)
//   - Esc             clear filter
//   - Enter           connect to selected host (exits TUI, runs `ssh`)
//   - q / Ctrl+C      quit without connecting
package main

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"charm.land/bubbles/v2/list"
	tea "charm.land/bubbletea/v2"
)

// SSHHost represents a single host entry parsed from ~/.ssh/config.
//
// All fields are strings. Empty fields mean "not set" -- buildSSHArgs only
// emits a flag for the field if it has a value.
type SSHHost struct {
	Alias        string
	HostName     string
	User         string
	Port         string
	IdentityFile string
	ProxyJump    string
}

// item wraps an SSHHost to satisfy list.Item.
type item struct{ host SSHHost }

// FilterValue is what bubbles uses for fuzzy filtering. Combining alias,
// hostname, and user lets the user search by any of them.
func (i item) FilterValue() string {
	return i.host.Alias + " " + i.host.HostName + " " + i.host.User
}

// itemDelegate renders each list item as two plain text lines:
//   line 1:  "  alias"  (or "> alias" for the selected item)
//   line 2:  "    hostname @ user"  (omitted if both hostname and user are empty)
//
// No colors, no borders -- just text and the `>` selection marker.
type itemDelegate struct{}

func (d itemDelegate) Height() int  { return 2 }
func (d itemDelegate) Spacing() int { return 1 } // blank line between items, matching DESIGN.md
func (d itemDelegate) Update(_ tea.Msg, _ *list.Model) tea.Cmd {
	return nil
}

// Render writes the item's text into w. Uses plain io.Writer -- *strings.Builder
// satisfies io.Writer, so callers can pass either.
func (d itemDelegate) Render(w io.Writer, m list.Model, index int, listItem list.Item) {
	i, ok := listItem.(item)
	if !ok {
		return
	}
	if index == m.Index() {
		fmt.Fprint(w, "> ")
	} else {
		fmt.Fprint(w, "  ")
	}
	fmt.Fprint(w, i.host.Alias)
	if i.host.HostName != "" || i.host.User != "" {
		fmt.Fprint(w, "\n    ")
		fmt.Fprint(w, i.host.HostName)
		if i.host.User != "" {
			fmt.Fprint(w, " @ ")
			fmt.Fprint(w, i.host.User)
		}
	}
}

// model is the top-level bubbletea Model. It embeds a bubbles list for the
// selectable host list and remembers which host the user picked (or nil if
// they quit).
type model struct {
	list         list.Model
	hosts        []SSHHost
	selectedHost *SSHHost // nil until user presses Enter
}

// initialModel converts parsed hosts into list items and configures the list.
func initialModel(hosts []SSHHost) model {
	items := make([]list.Item, len(hosts))
	for i, h := range hosts {
		items[i] = item{host: h}
	}

	const defaultWidth, defaultHeight = 80, 24
	l := list.New(items, itemDelegate{}, defaultWidth, defaultHeight)
	l.Title = fmt.Sprintf("sshmenu (%d hosts)", len(hosts))
	l.SetFilteringEnabled(true)
	l.SetShowStatusBar(false)
	l.SetShowHelp(true)

	return model{
		list:  l,
		hosts: hosts,
	}
}

// Init implements tea.Model.
func (m model) Init() tea.Cmd { return nil }

// Update handles key events. We intercept "enter" (to capture the selected
// host and quit), "q" and "esc" (to quit, but only when NOT in filter mode --
// when filtering, those keys should be typed into the filter input or clear
// it). All other messages are forwarded to the embedded list so it can handle
// WindowSize, j/k, /, filter typing, etc.
func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyPressMsg:
		switch msg.String() {
		case "enter":
			if i, ok := m.list.SelectedItem().(item); ok {
				m.selectedHost = &i.host
				return m, tea.Quit
			}
		case "q", "esc":
			if m.list.FilterState() != list.Filtering {
				return m, tea.Quit
			}
		}
	}

	var cmd tea.Cmd
	m.list, cmd = m.list.Update(msg)
	return m, cmd
}

// View renders the TUI into a tea.View with the alternate screen enabled.
// In v2, View() returns tea.View (not string) and AltScreen is a field on
// the returned view rather than a program option.
func (m model) View() tea.View {
	v := tea.NewView(m.list.View())
	v.AltScreen = true
	return v
}

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

// buildSSHArgs assembles the argv for `ssh` from a parsed host.
//
// - Port "22" is the ssh default, so we omit -p for it.
// - IdentityFile with a leading "~" is left as-is: ssh itself expands tilde.
// - If HostName is empty, we use Alias -- OpenSSH will resolve the alias
//   from the user's config (including any Match/Include blocks we don't parse).
func buildSSHArgs(host SSHHost) []string {
	var args []string
	if host.Port != "" && host.Port != "22" {
		args = append(args, "-p", host.Port)
	}
	if host.User != "" {
		args = append(args, "-l", host.User)
	}
	if host.IdentityFile != "" {
		args = append(args, "-i", host.IdentityFile)
	}
	if host.ProxyJump != "" {
		args = append(args, "-J", host.ProxyJump)
	}
	if host.HostName != "" {
		args = append(args, host.HostName)
	} else {
		args = append(args, host.Alias)
	}
	return args
}

// connectSSH launches `ssh` as a child process with full terminal I/O
// passthrough. Using os/exec on all platforms (rather than syscall.Exec
// on Unix) keeps the code in a single file and cross-compiles cleanly to
// Windows -- where syscall.Exec doesn't exist. Behavior is essentially
// identical to the user: stdin/stdout/stderr go straight through, signals
// are handled by the child ssh.
func connectSSH(host SSHHost) error {
	if _, err := exec.LookPath("ssh"); err != nil {
		return fmt.Errorf("ssh not found in PATH: %w", err)
	}
	cmd := exec.Command("ssh", buildSSHArgs(host)...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func main() {
	hosts, err := parseSSHConfig()
	if err != nil {
		fmt.Fprintf(os.Stderr, "sshmenu: %v\n", err)
		os.Exit(1)
	}
	if len(hosts) == 0 {
		fmt.Println("sshmenu: no hosts found in ~/.ssh/config")
		os.Exit(0)
	}

	p := tea.NewProgram(initialModel(hosts))
	finalModel, err := p.Run()
	if err != nil {
		fmt.Fprintf(os.Stderr, "sshmenu: %v\n", err)
		os.Exit(1)
	}

	fm, ok := finalModel.(model)
	if !ok || fm.selectedHost == nil {
		// User quit without picking a host.
		return
	}
	if err := connectSSH(*fm.selectedHost); err != nil {
		fmt.Fprintf(os.Stderr, "sshmenu: %v\n", err)
		os.Exit(1)
	}
}
