package main

import (
	"fmt"
	"io"
	"os"
	"strings"
	"unicode/utf8"

	"golang.org/x/term"
)

// uiState holds the runtime state of the TUI.
type uiState struct {
	items      []ListItem
	filtered   []int // indices into items, in display order
	cursor     int   // index into filtered
	filter     strings.Builder
	offset     int // index into filtered of the first visible row
	aliasWidth int // longest alias in current filtered list, for alignment
}

// newUIState allocates a fresh uiState with an empty filter list and
// no filter text. The caller must invoke applyFilter() to populate
// `filtered` based on the initial (empty) filter string.
func newUIState(items []ListItem) *uiState {
	return &uiState{
		items:    items,
		filtered: make([]int, 0, len(items)),
	}
}

// itemFilterString returns the lowercase text used for substring matching
// against the user's filter. For SSH hosts it joins alias/user/host/port
// (matching the original hostFilterString behavior); for Launchers it
// joins the display name and the command string.
func itemFilterString(it ListItem) string {
	switch it.Kind {
	case itemSSH:
		parts := make([]string, 0, 4)
		if it.Host.Alias != "" {
			parts = append(parts, it.Host.Alias)
		}
		if it.Host.User != "" {
			parts = append(parts, it.Host.User)
		}
		if it.Host.HostName != "" {
			parts = append(parts, it.Host.HostName)
		}
		if it.Host.Port != "" && it.Host.Port != "22" {
			parts = append(parts, it.Host.Port)
		}
		return strings.ToLower(strings.Join(parts, " "))
	case itemLauncher:
		return strings.ToLower(it.Alias + " " + it.Launcher.Command)
	default:
		return strings.ToLower(it.Alias)
	}
}

// applyFilter rebuilds the filtered slice from the current filter text.
// It also clamps the cursor into the valid range. It does NOT adjust the
// scroll offset; that is the caller's responsibility (draw does it).
func (s *uiState) applyFilter() {
	s.filtered = s.filtered[:0]
	q := strings.ToLower(s.filter.String())
	for i, it := range s.items {
		if q == "" || strings.Contains(itemFilterString(it), q) {
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
		if w := len(s.items[idx].Alias); w > s.aliasWidth {
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
// row begins with a 3-character selection marker, then the alias in
// brackets (padded to aliasWidth) and either a detail string (for SSH
// hosts) or the launcher command (for Launchers).
func drawRow(w io.Writer, it ListItem, selected bool, aliasWidth int) {
	if selected {
		fmt.Fprint(w, "-> ")
	} else {
		fmt.Fprint(w, "   ")
	}
	fmt.Fprint(w, "[")
	fmt.Fprint(w, it.Alias)
	// Pad inside brackets to align the closing bracket.
	if pad := aliasWidth - len(it.Alias); pad > 0 {
		fmt.Fprint(w, strings.Repeat(" ", pad))
	}
	fmt.Fprint(w, "]")
	switch it.Kind {
	case itemSSH:
		if detail := buildDetail(it.Host); detail != "" {
			fmt.Fprint(w, " ")
			fmt.Fprint(w, detail)
		}
	case itemLauncher:
		fmt.Fprint(w, " -> ")
		fmt.Fprint(w, it.Launcher.Command)
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
	listRows := height - 2 - filterBarHeight
	if listRows < 1 {
		listRows = 1
	}
	s.adjustOffset(listRows)

	// Compute the position (in filtered) of the first Launcher, if any.
	// We only show the divider when both sections are present in the
	// current filter window.
	separatorIdx := -1
	hasSSH := false
	for i, idx := range s.filtered {
		switch s.items[idx].Kind {
		case itemSSH:
			hasSSH = true
		case itemLauncher:
			if separatorIdx == -1 {
				separatorIdx = i
			}
		}
	}
	showSeparator := hasSSH && separatorIdx >= 0

	var b strings.Builder
	b.WriteString("\033[2J\033[?25l") // clear screen + hide cursor

	// Title at row 1.
	b.WriteString("\033[1;1H  sshmenu")

	// List rows start at row 3. The divider consumes one iteration on
	// its own: when we reach the first launcher, we draw the divider
	// and skip advancing `itemIdx`, so the next iteration renders the
	// launcher on the following row.
	itemIdx := s.offset
	separatorDrawn := false
	for i := 0; i < listRows; i++ {
		row := 3 + i
		b.WriteString(fmt.Sprintf("\033[%d;1H", row))
		if itemIdx < 0 || itemIdx >= len(s.filtered) {
			continue
		}
		if showSeparator && !separatorDrawn && itemIdx == separatorIdx {
			b.WriteString(strings.Repeat("-", width))
			separatorDrawn = true
			// Hold `itemIdx` so the next iteration draws the launcher.
			continue
		}
		drawRow(&b, s.items[s.filtered[itemIdx]], itemIdx == s.cursor, s.aliasWidth)
		itemIdx++
	}

	// Filter bar.
	if filterBarHeight > 0 {
		sepRow := 3 + listRows
		b.WriteString(fmt.Sprintf("\033[%d;1H", sepRow))
		b.WriteString(strings.Repeat("-", width))
		b.WriteString(fmt.Sprintf("\033[%d;1H", sepRow+1))
		b.WriteString("  filter: ")
		b.WriteString(s.filter.String())
		fmt.Fprintf(&b, " (%d/%d)", s.cursor+1, len(s.filtered))
	}

	_, _ = io.WriteString(w, b.String())
}

// runTUI enters raw mode, runs the event loop, and returns either the
// item the user selected or nil if the user quit.
func runTUI(items []ListItem, history []string) (*ListItem, error) {
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

	state := newUIState(items)
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
			it := state.items[state.filtered[state.cursor]]
			saveHistory(updateHistory(history, it.Alias))
			return &it, nil
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
