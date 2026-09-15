//go:build darwin

package platform

import (
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"os/exec"
	"strings"
)

// securityTool is the macOS Keychain command-line interface. The daemon is
// built with CGO_ENABLED=0, which rules out calling Security.framework
// directly.
const securityTool = "/usr/bin/security"

// errSecItemNotFound is the exit status security(1) reports for a missing item.
const errSecItemNotFound = 44

// maxSecretBytes keeps the hex-encoded secret well inside one line of
// `security -i` input.
const maxSecretBytes = 512

// runFunc runs securityTool with args, feeding stdin, and returns its stdout,
// stderr and exit status. err is non-nil only when the tool could not be run.
type runFunc func(ctx context.Context, stdin string, args ...string) (stdout, stderr string, exit int, err error)

type keychain struct {
	service string
	run     runFunc
}

func newKeychain(service string) *keychain {
	return &keychain{service: service, run: runSecurity}
}

func runSecurity(ctx context.Context, stdin string, args ...string) (string, string, int, error) {
	cmd := exec.CommandContext(ctx, securityTool, args...)
	cmd.Stdin = strings.NewReader(stdin)
	var out, errOut bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errOut
	err := RunInGroup(cmd)
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return out.String(), errOut.String(), exitErr.ExitCode(), nil
	}
	if err != nil {
		return "", "", -1, fmt.Errorf("running %s: %w", securityTool, err)
	}
	return out.String(), errOut.String(), 0, nil
}

func (k *keychain) Get(ctx context.Context, name string) (string, error) {
	if err := checkSecretName(name); err != nil {
		return "", err
	}
	out, errOut, exit, err := k.run(ctx, "", "find-generic-password", "-s", k.service, "-a", name, "-w")
	if err != nil {
		return "", err
	}
	if err := k.status("reading", name, exit, errOut); err != nil {
		return "", err
	}
	return strings.TrimSuffix(out, "\n"), nil
}

// Set never puts the secret in an argument vector, where any local user could
// read it from the process table. `security -i` reads the command from stdin,
// and -X carries the value hex-encoded so no character needs quoting.
func (k *keychain) Set(ctx context.Context, name, secret string) error {
	if err := checkSecretName(name); err != nil {
		return err
	}
	if err := checkSecretValue(secret); err != nil {
		return fmt.Errorf("storing secret %q: %w", name, err)
	}
	if len(secret) > maxSecretBytes {
		return fmt.Errorf("storing secret %q: longer than %d bytes", name, maxSecretBytes)
	}
	line := fmt.Sprintf("add-generic-password -U -s %s -a %s -X %s\n", k.service, name, hex.EncodeToString([]byte(secret)))
	_, errOut, exit, err := k.run(ctx, line, "-i")
	if err != nil {
		return err
	}
	return k.status("storing", name, exit, errOut)
}

func (k *keychain) Delete(ctx context.Context, name string) error {
	if err := checkSecretName(name); err != nil {
		return err
	}
	_, errOut, exit, err := k.run(ctx, "", "delete-generic-password", "-s", k.service, "-a", name)
	if err != nil {
		return err
	}
	return k.status("deleting", name, exit, errOut)
}

func (k *keychain) status(verb, name string, exit int, stderr string) error {
	switch exit {
	case 0:
		return nil
	case errSecItemNotFound:
		return fmt.Errorf("%s secret %q in the Keychain (service %q): %w", verb, name, k.service, ErrSecretNotFound)
	default:
		return fmt.Errorf("%s secret %q in the Keychain (service %q): security exited %d: %s", verb, name, k.service, exit, strings.TrimSpace(stderr))
	}
}
