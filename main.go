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
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"golang.org/x/term"
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

// keyKind classifies a key event read from the terminal.
type keyKind int

const (
	keyRune keyKind = iota
	keyUp
	keyDown
	keyEnter
	keyBackspace
	keyEsc
	keyQuit
	keyCtrlC
)

// keyEvent represents one parsed key (or key chord) from the terminal.
// `special` is keyRune for printable characters; in that case `r` holds
// the character. For all other keyKinds, `r` is zero.
type keyEvent struct {
	special keyKind
	r       rune
}

// readKey reads one key event from stdin in raw mode. It handles plain
// printable characters, Enter, Backspace, Ctrl-C, 'q', and the CSI escape
// sequences for arrow keys (ESC [ A / ESC [ B). Lone ESC is detected via
// a short read deadline -- if no further byte arrives within 10ms, the
// event is reported as keyEsc.
func readKey() (keyEvent, error) {
	var b [1]byte
	for {
		n, err := os.Stdin.Read(b[:])
		if err != nil {
			return keyEvent{}, err
		}
		if n == 0 {
			continue
		}
		switch b[0] {
		case 0x03:
			return keyEvent{special: keyCtrlC}, nil
		case 0x0D, 0x0A:
			return keyEvent{special: keyEnter}, nil
		case 0x7F, 0x08:
			return keyEvent{special: keyBackspace}, nil
		case 'q':
			return keyEvent{special: keyQuit}, nil
		case 0x1B:
			return readEscapeSequence()
		default:
			if b[0] >= 0x20 && b[0] <= 0x7E {
				return keyEvent{special: keyRune, r: rune(b[0])}, nil
			}
			// Ignore other control bytes.
		}
	}
}

// readEscapeSequence handles the bytes that follow an initial ESC (0x1B).
// It uses a 10ms peek timeout to distinguish a bare ESC keypress from the
// start of a CSI sequence (e.g. ESC [ A for the Up arrow).
func readEscapeSequence() (keyEvent, error) {
	_ = os.Stdin.SetReadDeadline(time.Now().Add(10 * time.Millisecond))
	var b [1]byte
	n, err := os.Stdin.Read(b[:])
	// Always clear the deadline, even on error.
	_ = os.Stdin.SetReadDeadline(time.Time{})

	if n == 0 {
		// Timeout: lone ESC.
		return keyEvent{special: keyEsc}, nil
	}
	if err != nil {
		return keyEvent{}, err
	}
	if b[0] != '[' {
		// Unknown ESC sequence; ignore.
		return readKey()
	}
	// Read the final byte of the CSI sequence. Once '[' has arrived, the
	// third byte always follows immediately, so a plain blocking read is
	// safe here.
	m, err := os.Stdin.Read(b[:])
	if err != nil {
		return keyEvent{}, err
	}
	if m == 0 {
		return keyEvent{special: keyEsc}, nil
	}
	switch b[0] {
	case 'A':
		return keyEvent{special: keyUp}, nil
	case 'B':
		return keyEvent{special: keyDown}, nil
	}
	// Unrecognised CSI sequence; ignore.
	return readKey()
}

// uiState holds the runtime state of the TUI.
type uiState struct {
	hosts      []SSHHost
	filtered   []int   // indices into hosts, in display order
	cursor     int     // index into filtered
	filter     strings.Builder
	offset     int     // index into filtered of the first visible row
	aliasWidth int     // longest alias in current filtered list, for alignment
}

// newUIState allocates a fresh uiState with an empty filter list and
// no filter text. The caller must invoke applyFilter() to populate
// `filtered` based on the initial (empty) filter string.
func newUIState(hosts []SSHHost) *uiState {
	return &uiState{
		hosts:    hosts,
		filtered: make([]int, 0, len(hosts)),
	}
}

// hostFilterString returns the lowercase text used for substring matching
// against the user's filter. Combining alias, user, hostname, and a
// non-default port lets the user search by any of them.
func hostFilterString(h SSHHost) string {
	parts := make([]string, 0, 4)
	if h.Alias != "" {
		parts = append(parts, h.Alias)
	}
	if h.User != "" {
		parts = append(parts, h.User)
	}
	if h.HostName != "" {
		parts = append(parts, h.HostName)
	}
	if h.Port != "" && h.Port != "22" {
		parts = append(parts, h.Port)
	}
	return strings.ToLower(strings.Join(parts, " "))
}

// applyFilter rebuilds the filtered slice from the current filter text.
// It also clamps the cursor into the valid range. It does NOT adjust the
// scroll offset; that is the caller's responsibility (draw does it).
func (s *uiState) applyFilter() {
	s.filtered = s.filtered[:0]
	q := strings.ToLower(s.filter.String())
	for i, h := range s.hosts {
		if q == "" || strings.Contains(hostFilterString(h), q) {
			s.filtered = append(s.filtered, i)
		}
	}
	switch {
	case len(s.filtered) == 0:
		s.cursor = 0
	case s.cursor >= len(s.filtered):
		s.cursor = len(s.filtered) - 1
	case s.cursor < 0:
		s.cursor = 0
	}
	// Calculate max alias width for alignment.
	s.aliasWidth = 0
	for _, idx := range s.filtered {
		if w := len(s.hosts[idx].Alias); w > s.aliasWidth {
			s.aliasWidth = w
		}
	}
}

// adjustOffset keeps the cursor visible within the given viewport height.
// visibleRows must be >= 1.
func (s *uiState) adjustOffset(visibleRows int) {
	if visibleRows < 1 {
		visibleRows = 1
	}
	if len(s.filtered) == 0 {
		s.offset = 0
		return
	}
	if s.cursor < s.offset {
		s.offset = s.cursor
		return
	}
	if s.cursor >= s.offset+visibleRows {
		s.offset = s.cursor - visibleRows + 1
	}
	if s.offset < 0 {
		s.offset = 0
	}
}

func (s *uiState) moveUp() {
	if s.cursor > 0 {
		s.cursor--
	}
}

func (s *uiState) moveDown() {
	if s.cursor < len(s.filtered)-1 {
		s.cursor++
	}
}

// buildDetail returns the "user@host[:port]" portion of a row, omitting
// fields that are empty and omitting :22 (the ssh default).
func buildDetail(h SSHHost) string {
	var b strings.Builder
	if h.User != "" {
		b.WriteString(h.User)
		b.WriteByte('@')
	}
	b.WriteString(h.HostName)
	if h.Port != "" && h.Port != "22" {
		b.WriteByte(':')
		b.WriteString(h.Port)
	}
	return b.String()
}

// drawRow writes one list row (without a trailing newline) into w. The
// row begins with `\033[K` to clear any stale characters from a previous
// frame, then a 3-character selection marker, then the alias in brackets
// and the detail text.
func drawRow(w io.Writer, h SSHHost, selected bool, aliasWidth int) {
	fmt.Fprint(w, "\033[K")
	if selected {
		fmt.Fprint(w, "-> ")
	} else {
		fmt.Fprint(w, "   ")
	}
	fmt.Fprint(w, "[")
	fmt.Fprint(w, h.Alias)
	// Pad inside brackets to align the closing bracket.
	if pad := aliasWidth - len(h.Alias); pad > 0 {
		fmt.Fprint(w, strings.Repeat(" ", pad))
	}
	fmt.Fprint(w, "]")
	if detail := buildDetail(h); detail != "" {
		fmt.Fprint(w, " ")
		fmt.Fprint(w, detail)
	}
}

// draw renders the full TUI frame to w. It builds the entire frame in a
// strings.Builder first and writes it in a single syscall to avoid
// partial-frame flicker.
func draw(w io.Writer, s *uiState, width, height int) {
	filterBarHeight := 0
	if s.filter.Len() > 0 {
		filterBarHeight = 2
	}
	// Title takes 2 lines (title + blank). Remaining space is for the list.
	listRows := height - 2 - filterBarHeight
	if listRows < 1 {
		listRows = 1
	}
	s.adjustOffset(listRows)

	var b strings.Builder
	b.WriteString("\033[H\033[?25l") // home + hide cursor

	// Title.
	b.WriteString("\033[K  连接到SSH:\r\n")
	b.WriteString("\033[K\r\n")

	// List rows.
	for i := 0; i < listRows; i++ {
		idx := s.offset + i
		if idx >= 0 && idx < len(s.filtered) {
			drawRow(&b, s.hosts[s.filtered[idx]], idx == s.cursor, s.aliasWidth)
		} else {
			b.WriteString("\033[K")
		}
		b.WriteString("\r\n")
	}

	// Filter bar.
	if filterBarHeight > 0 {
		b.WriteString("\033[K")
		b.WriteString(strings.Repeat("-", width))
		b.WriteString("\r\n")
		b.WriteString("\033[K  filter: ")
		b.WriteString(s.filter.String())
		fmt.Fprintf(&b, " (%d/%d)", s.cursor+1, len(s.filtered))
	}

	_, _ = io.WriteString(w, b.String())
}

// runTUI enters raw mode, runs the event loop, and returns either the
// host the user selected or nil if the user quit.
func runTUI(hosts []SSHHost) (*SSHHost, error) {
	fd := int(os.Stdin.Fd())
	oldState, err := term.MakeRaw(fd)
	if err != nil {
		return nil, fmt.Errorf("enter raw mode: %w", err)
	}
	// Single defer that runs on every exit path (return, panic, etc.).
	defer func() {
		_ = term.Restore(fd, oldState)
		fmt.Print("\033[?25h\033[?1049l")
	}()

	// Enter alternate screen, clear it, home the cursor.
	fmt.Print("\033[?1049h\033[2J\033[H")

	state := newUIState(hosts)
	state.applyFilter()

	width, height, err := term.GetSize(fd)
	if err != nil || width <= 0 {
		width = 80
	}
	if err != nil || height <= 0 {
		height = 24
	}
	draw(os.Stdout, state, width, height)

	for {
		ev, err := readKey()
		if err != nil {
			return nil, err
		}
		dirty := false
		switch ev.special {
		case keyQuit, keyCtrlC:
			return nil, nil
		case keyEnter:
			if len(state.filtered) == 0 {
				continue
			}
			host := state.hosts[state.filtered[state.cursor]]
			return &host, nil
		case keyEsc:
			if state.filter.Len() > 0 {
				state.filter.Reset()
				state.applyFilter()
				dirty = true
			} else {
				return nil, nil
			}
		case keyUp:
			state.moveUp()
			dirty = true
		case keyDown:
			state.moveDown()
			dirty = true
		case keyBackspace:
			if state.filter.Len() > 0 {
				cur := state.filter.String()
				// Trim the last rune (handles multi-byte UTF-8).
				r, size := utf8.DecodeLastRuneInString(cur)
				if r != utf8.RuneError {
					state.filter.Reset()
					state.filter.WriteString(cur[:len(cur)-size])
					state.applyFilter()
					dirty = true
				}
			}
		case keyRune:
			switch ev.r {
			case 'j':
				state.moveDown()
				dirty = true
			case 'k':
				state.moveUp()
				dirty = true
			default:
				state.filter.WriteRune(ev.r)
				state.applyFilter()
				dirty = true
			}
		}
		if dirty {
			width, height, err = term.GetSize(fd)
			if err != nil || width <= 0 {
				width = 80
			}
			if err != nil || height <= 0 {
				height = 24
			}
			draw(os.Stdout, state, width, height)
		}
	}
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
