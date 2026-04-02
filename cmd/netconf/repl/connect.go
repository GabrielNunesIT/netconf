package repl

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	netconf "github.com/GabrielNunesIT/netconf"
	"github.com/GabrielNunesIT/netconf/client"
	gossh "golang.org/x/crypto/ssh"

	"github.com/chzyer/readline"
)

// handleConnect parses connect flags and establishes a NETCONF session over SSH.
//
// Flags:
//
//	--host <host>          remote hostname or IP (required)
//	--port <n>             SSH port (default: 830)
//	--user <user>          SSH username (required)
//	--password <pw>        SSH password (prompted securely if omitted)
//	--key <path>           path to PEM private key file (alternative to password)
//	--insecure             skip host key verification (dev use only)
func handleConnect(args []string, rl *readline.Instance, sess *Session, outW, errW io.Writer) error {
	if sess.Connected() {
		fmt.Fprintf(errW, "already connected to %s — use 'disconnect' first\n", sess.Host())
		return nil
	}

	fs := flag.NewFlagSet("connect", flag.ContinueOnError)
	fs.SetOutput(errW)

	host := fs.String("host", "", "remote hostname or IP (required)")
	port := fs.Int("port", 830, "SSH port")
	user := fs.String("user", "", "SSH username (required)")
	password := fs.String("password", "", "SSH password (prompted if omitted)")
	keyPath := fs.String("key", "", "path to PEM private key file")
	insecure := fs.Bool("insecure", false, "skip host key verification (dev use)")

	if err := fs.Parse(args); err != nil {
		// flag package already wrote the error to errW.
		return nil //nolint:nilerr // REPL: parse errors are printed to errW, not returned
	}

	if *host == "" {
		fmt.Fprintf(errW, "connect: --host is required\n")
		return nil
	}
	if *user == "" {
		fmt.Fprintf(errW, "connect: --user is required\n")
		return nil
	}

	addr := *host + ":" + strconv.Itoa(*port)

	// Build auth methods.
	var authMethods []gossh.AuthMethod

	if *keyPath != "" {
		keyBytes, err := os.ReadFile(*keyPath)
		if err != nil {
			fmt.Fprintf(errW, "connect: read key %q: %v\n", *keyPath, err)
			return nil
		}
		signer, err := gossh.ParsePrivateKey(keyBytes)
		if err != nil {
			fmt.Fprintf(errW, "connect: parse key %q: %v\n", *keyPath, err)
			return nil
		}
		authMethods = append(authMethods, gossh.PublicKeys(signer))
	}

	if *password != "" {
		authMethods = append(authMethods, gossh.Password(*password))
	} else if *keyPath == "" {
		// No password flag and no key — prompt securely.
		pw, err := promptPassword(rl, *user, *host)
		if err != nil {
			fmt.Fprintf(errW, "connect: password prompt: %v\n", err)
			return nil
		}
		authMethods = append(authMethods, gossh.Password(pw))
	}

	if len(authMethods) == 0 {
		fmt.Fprintf(errW, "connect: no auth method configured\n")
		return nil
	}

	// Host key callback.
	var hostKeyCallback gossh.HostKeyCallback
	if *insecure {
		fmt.Fprintf(errW, "warning: --insecure disables host key verification\n")
		hostKeyCallback = gossh.InsecureIgnoreHostKey() //nolint:gosec // intentional for dev use
	} else {
		fmt.Fprintf(errW, "warning: host key verification is not implemented; connection is vulnerable to MITM attacks\n")
		hostKeyCallback = gossh.InsecureIgnoreHostKey() //nolint:gosec
	}

	sshConfig := &gossh.ClientConfig{
		User:            *user,
		Auth:            authMethods,
		HostKeyCallback: hostKeyCallback,
		Timeout:         10 * time.Second,
	}

	caps := netconf.NewCapabilitySet([]string{
		netconf.BaseCap10,
		netconf.BaseCap11,
	})

	fmt.Fprintf(outW, "connecting to %s...\n", addr)

	cli, err := client.Dial(context.Background(), addr, sshConfig, caps)
	if err != nil {
		fmt.Fprintf(errW, "connect: %v\n", err)
		return nil
	}

	sess.cli = cli
	sess.host = strings.TrimSuffix(addr, ":830") // omit default port for cleaner prompt
	if *port != 830 {
		sess.host = addr
	}

	// Print connection confirmation.
	fmt.Fprintf(outW, "connected to %s (session-id: %d)\n", addr, cli.SessionID())

	return nil
}

// handleDisconnect closes the current NETCONF session.
func handleDisconnect(sess *Session, outW, errW io.Writer) error {
	if !sess.Connected() {
		fmt.Fprintf(errW, "not connected\n")
		return nil
	}
	if err := sess.Close(); err != nil {
		fmt.Fprintf(errW, "disconnect: close-session: %v\n", err)
		// Session state is cleared by Close() regardless of error.
	}
	fmt.Fprintf(outW, "disconnected\n")
	return nil
}

// promptPassword uses readline's masked input to prompt for a password.
// Falls back to a plain fmt.Scan prompt when rl is nil (tests).
func promptPassword(rl *readline.Instance, user, host string) (string, error) {
	prompt := fmt.Sprintf("%s@%s password: ", user, host)
	if rl == nil {
		// Test path: readline not available.
		fmt.Print(prompt)
		var pw string
		_, err := fmt.Scan(&pw)
		return pw, err
	}
	// Save and restore the current prompt so the REPL prompt isn't corrupted.
	oldPrompt := rl.Config.Prompt
	rl.SetPrompt(prompt)
	defer rl.SetPrompt(oldPrompt)

	pw, err := rl.ReadPassword(prompt)
	if err != nil {
		return "", err
	}
	return string(pw), nil
}
