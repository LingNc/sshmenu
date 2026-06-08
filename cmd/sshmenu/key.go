package main

import (
	"os"
	"time"
)

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
