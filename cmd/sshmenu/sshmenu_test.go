package main

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestParseKeyValue(t *testing.T) {
	cases := []struct {
		in  string
		key string
		val string
	}{
		{"HostName example.com", "hostname", "example.com"},
		{"User alice", "user", "alice"},
		{"Port 2222", "port", "2222"},
		{"HostName example.com # my server", "hostname", "example.com"},
		{`HostName "host#name"`, "hostname", "host#name"},
		{"IdentityFile ~/.ssh/id_rsa", "identityfile", "~/.ssh/id_rsa"},
		{"JustKeyNoSpace", "", ""},
	}
	for _, c := range cases {
		k, v := parseKeyValue(c.in)
		if k != c.key || v != c.val {
			t.Errorf("parseKeyValue(%q) = (%q, %q), want (%q, %q)", c.in, k, v, c.key, c.val)
		}
	}
}

func TestStripComment(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"example.com", "example.com"},
		{"example.com # comment", "example.com"},
		{`"host#name"`, `"host#name"`},
		{"a # b # c", "a"},
		{`"a" # b`, `"a"`},
		{"no#hash", "no#hash"},    // unquoted # without leading whitespace is preserved
		{"no #hash", "no"},        // unquoted # with leading whitespace starts a comment
		{"#leading", ""},          // # at position 0 of value (right after keyword) is a comment
		{`a#b #c`, "a#b"},         // only the second # (with leading space) is a comment
		{`a b#c`, "a b#c"},        // # attached to non-space char is preserved
	}
	for _, c := range cases {
		got := stripComment(c.in)
		if got != c.want {
			t.Errorf("stripComment(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestStripQuotes(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{`"hello"`, "hello"},
		{"plain", "plain"},
		{`"unbalanced`, `"unbalanced`},
		{`a"`, `a"`},
		{`""`, ""},
	}
	for _, c := range cases {
		got := stripQuotes(c.in)
		if got != c.want {
			t.Errorf("stripQuotes(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestBuildSSHArgs(t *testing.T) {
	h := SSHHost{
		Alias:        "dev",
		HostName:     "dev.example.com",
		User:         "alice",
		Port:         "2222",
		IdentityFile: "~/.ssh/id_rsa",
		ProxyJump:    "jump.example.com",
	}
	args := buildSSHArgs(h)
	want := []string{"-p", "2222", "-l", "alice", "-i", "~/.ssh/id_rsa", "-J", "jump.example.com", "dev.example.com"}
	if !reflect.DeepEqual(args, want) {
		t.Errorf("full args = %v, want %v", args, want)
	}

	h2 := SSHHost{Alias: "x", Port: "22"}
	args2 := buildSSHArgs(h2)
	if !reflect.DeepEqual(args2, []string{"x"}) {
		t.Errorf("port=22: args = %v, want [x]", args2)
	}

	h3 := SSHHost{Alias: "alias-only"}
	args3 := buildSSHArgs(h3)
	if !reflect.DeepEqual(args3, []string{"alias-only"}) {
		t.Errorf("empty hostname: args = %v, want [alias-only]", args3)
	}

	// No fields at all -> just the alias.
	h4 := SSHHost{Alias: "bare"}
	args4 := buildSSHArgs(h4)
	if !reflect.DeepEqual(args4, []string{"bare"}) {
		t.Errorf("bare: args = %v, want [bare]", args4)
	}
}

// TestParseSSHConfig_WithTempHome runs the full parser against a temp config.
func TestParseSSHConfig_WithTempHome(t *testing.T) {
	// Snapshot and restore env.
	origHome, origHomeEnv := os.UserHomeDir()
	t.Setenv("HOME", origHome) // explicit on Unix
	_ = origHomeEnv

	tmp := t.TempDir()
	sshDir := filepath.Join(tmp, ".ssh")
	if err := os.MkdirAll(sshDir, 0o700); err != nil {
		t.Fatal(err)
	}
	cfg := `# top comment
Host *
   User root
   Port 22
   IdentityFile ~/.ssh/id_default

# dev box
Host dev
   HostName 10.0.0.1
   User alice
   Port 2222
   IdentityFile ~/.ssh/id_dev

# multi-host block
Host staging prod
   HostName internal.example.com
   User deploy

Host web
   HostName "web#01" # literal hash in hostname
   Port 2222

# duplicate key, first wins
Host dup
   HostName first.example.com
   HostName second.example.com
`
	cfgPath := filepath.Join(sshDir, "config")
	if err := os.WriteFile(cfgPath, []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}

	t.Setenv("HOME", tmp)
	// On Windows, USERPROFILE is what os.UserHomeDir checks.
	t.Setenv("USERPROFILE", tmp)

	hosts, err := parseSSHConfig()
	if err != nil {
		t.Fatalf("parseSSHConfig: %v", err)
	}

	// Build a quick lookup.
	byAlias := map[string]SSHHost{}
	for _, h := range hosts {
		if _, dup := byAlias[h.Alias]; dup {
			t.Errorf("duplicate alias in result: %q", h.Alias)
		}
		byAlias[h.Alias] = h
	}

	// "*" must not appear.
	if _, ok := byAlias["*"]; ok {
		t.Error("Host * should not appear in result")
	}

	// dev: gets global defaults from Host *, then overridden.
	if h := byAlias["dev"]; h.User != "alice" || h.HostName != "10.0.0.1" || h.Port != "2222" || h.IdentityFile != "~/.ssh/id_dev" {
		t.Errorf("dev = %+v", h)
	}

	// staging/prod: share host config, get User "deploy" from block, not root from global.
	if h := byAlias["staging"]; h.User != "deploy" || h.HostName != "internal.example.com" {
		t.Errorf("staging = %+v", h)
	}
	if h := byAlias["prod"]; h.User != "deploy" || h.HostName != "internal.example.com" {
		t.Errorf("prod = %+v", h)
	}

	// web: hostname with literal '#' inside quotes preserved.
	if h := byAlias["web"]; h.HostName != "web#01" || h.Port != "2222" {
		t.Errorf("web = %+v (want HostName=web#01 Port=2222)", h)
	}

	// dup: first HostName wins.
	if h := byAlias["dup"]; h.HostName != "first.example.com" {
		t.Errorf("dup = %+v (want first.example.com)", h)
	}

	if len(hosts) != 5 {
		t.Errorf("got %d hosts, want 5; hosts = %+v", len(hosts), hosts)
	}
}

func TestParseSSHConfig_NoFile(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)
	hosts, err := parseSSHConfig()
	if err != nil {
		t.Fatalf("expected nil error for missing file, got %v", err)
	}
	if hosts == nil {
		t.Error("expected non-nil empty slice")
	}
	if len(hosts) != 0 {
		t.Errorf("expected empty, got %+v", hosts)
	}
}

// TestParseSSHConfig_HostStarAfterHost covers the position-independence fix:
// when "Host *" appears AFTER a regular host block, the global defaults must
// still be applied to that earlier host (OpenSSH behavior).
func TestParseSSHConfig_HostStarAfterHost(t *testing.T) {
	tmp := t.TempDir()
	sshDir := filepath.Join(tmp, ".ssh")
	if err := os.MkdirAll(sshDir, 0o700); err != nil {
		t.Fatal(err)
	}
	cfg := `Host early
   HostName early.example.com

Host *
   User shared
   Port 2222
`
	cfgPath := filepath.Join(sshDir, "config")
	if err := os.WriteFile(cfgPath, []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}

	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)

	hosts, err := parseSSHConfig()
	if err != nil {
		t.Fatalf("parseSSHConfig: %v", err)
	}

	byAlias := map[string]SSHHost{}
	for _, h := range hosts {
		byAlias[h.Alias] = h
	}

	if h, ok := byAlias["early"]; !ok {
		t.Errorf("early host missing; hosts = %+v", hosts)
	} else {
		// "early" was declared before "Host *", so it should still inherit
		// User and Port from the global default block.
		if h.User != "shared" {
			t.Errorf("early.User = %q, want %q", h.User, "shared")
		}
		if h.Port != "2222" {
			t.Errorf("early.Port = %q, want %q", h.Port, "2222")
		}
		if h.HostName != "early.example.com" {
			t.Errorf("early.HostName = %q, want %q", h.HostName, "early.example.com")
		}
	}
}

// silence unused import warning if strings is unused after edits
var _ = strings.TrimSpace

func TestReorderItems(t *testing.T) {
	items := []ListItem{
		{Kind: itemSSH, Alias: "alpha"},
		{Kind: itemSSH, Alias: "beta"},
		{Kind: itemLauncher, Alias: "bash"},
		{Kind: itemLauncher, Alias: "pwsh"},
	}

	// History with mixed SSH/Launcher aliases.
	got := reorderItems(items, []string{"bash", "alpha"})
	want := []ListItem{
		{Kind: itemLauncher, Alias: "bash"},
		{Kind: itemSSH, Alias: "alpha"},
		{Kind: itemSSH, Alias: "beta"},
		{Kind: itemLauncher, Alias: "pwsh"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("reorderItems(matched) = %+v, want %+v", got, want)
	}

	// Empty history.
	got2 := reorderItems(items, nil)
	if !reflect.DeepEqual(got2, items) {
		t.Errorf("reorderItems(empty) = %+v, want %+v", got2, items)
	}

	// No match.
	got3 := reorderItems(items, []string{"missing"})
	if !reflect.DeepEqual(got3, items) {
		t.Errorf("reorderItems(no match) = %+v, want %+v", got3, items)
	}
}

func TestParseLaunchers(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmp)
	t.Setenv("HOME", tmp)
	t.Setenv("APPDATA", tmp)
	t.Setenv("USERPROFILE", tmp)

	// File does not exist: returns nil, no error.
	got, err := parseLaunchers()
	if err != nil {
		t.Fatalf("parseLaunchers (no file): %v", err)
	}
	if got != nil {
		t.Errorf("expected nil, got %+v", got)
	}

	// Write a valid config.
	cfg := `# comment
bash=/bin/bash
zsh = /bin/zsh

# empty name and empty command ignored
=emptyname
emptycmd=

powershell=pwsh
`
	cfgPath := filepath.Join(tmp, "sshmenu", "launchers")
	if err := os.MkdirAll(filepath.Dir(cfgPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cfgPath, []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err = parseLaunchers()
	if err != nil {
		t.Fatalf("parseLaunchers: %v", err)
	}
	want := []Launcher{
		{Name: "bash", Command: "/bin/bash"},
		{Name: "zsh", Command: "/bin/zsh"},
		{Name: "powershell", Command: "pwsh"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("parseLaunchers = %+v, want %+v", got, want)
	}
}

func TestHistoryRoundTrip(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmp)
	t.Setenv("HOME", tmp)
	t.Setenv("APPDATA", tmp)
	t.Setenv("USERPROFILE", tmp)

	// File does not exist yet: loadHistory returns nil.
	if got := loadHistory(); got != nil {
		t.Errorf("loadHistory before save = %v, want nil", got)
	}

	saveHistory([]string{"myhost"})

	if got := loadHistory(); len(got) != 1 || got[0] != "myhost" {
		t.Errorf("loadHistory after save = %v, want [myhost]", got)
	}

	// Multiple entries.
	saveHistory([]string{"first", "second", "third"})
	if got := loadHistory(); len(got) != 3 || got[0] != "first" || got[2] != "third" {
		t.Errorf("loadHistory multi = %v, want [first second third]", got)
	}
}

func TestUpdateHistory(t *testing.T) {
	// New entry prepended.
	got := updateHistory([]string{"a", "b"}, "c")
	if len(got) != 3 || got[0] != "c" || got[1] != "a" || got[2] != "b" {
		t.Errorf("updateHistory new = %v, want [c a b]", got)
	}

	// Existing entry moved to front.
	got = updateHistory([]string{"a", "b", "c"}, "b")
	if len(got) != 3 || got[0] != "b" || got[1] != "a" || got[2] != "c" {
		t.Errorf("updateHistory move = %v, want [b a c]", got)
	}

	// Caps at maxHistoryEntries.
	long := make([]string, maxHistoryEntries)
	for i := range long {
		long[i] = string(rune('a' + i))
	}
	got = updateHistory(long, "new")
	if len(got) != maxHistoryEntries {
		t.Errorf("updateHistory cap len = %d, want %d", len(got), maxHistoryEntries)
	}
	if got[0] != "new" {
		t.Errorf("updateHistory cap first = %v, want new", got[0])
	}
}
