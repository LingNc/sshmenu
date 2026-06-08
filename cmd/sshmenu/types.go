package main

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
