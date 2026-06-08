package main

import (
	"fmt"
	"os"
	"os/exec"
)

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
