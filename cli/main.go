// Command helm is a command-line client of helmstudio: sign in, list workflows, run one and follow it.
package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"strings"
)

const usage = `usage:
  helm login --url <base> --email <email> [--api-key-file <path>]   password: HELM_PASSWORD or a prompt
  helm run <workflow_id> [--session <id|new>] [-p node.param=value ...] [--out <dir>]
  helm takes [--kind image|video|audio] [--source create|upload] [--starred] [--session <id>] [--q <text>] [--limit n] [--cursor c]
  helm upload <file>                  a png, jpeg or webp image, into the gallery
  helm workflows
  helm whoami
  helm logout

The client API key comes from HELM_API_KEY, else the file given to helm login.
`

func main() {
	stop := make(chan os.Signal, 2)
	signal.Notify(stop, os.Interrupt)
	os.Exit(helm(os.Args[1:], os.Stdin, os.Stdout, os.Stderr, stop))
}

// helm runs one command and returns the exit code: 0 ok, 1 failed, 2 usage.
func helm(args []string, stdin io.Reader, stdout, stderr io.Writer, stop <-chan os.Signal) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, usage)
		return 2
	}
	var err error
	switch args[0] {
	case "login":
		err = login(args[1:], stdin, stdout, stderr)
	case "run":
		err = runCmd(args[1:], stdout, stderr, stop)
	case "takes":
		err = takesCmd(args[1:], stdout, stderr)
	case "upload":
		err = uploadCmd(args[1:], stdout)
	case "workflows":
		err = workflows(stdout)
	case "whoami":
		err = whoami(stdout)
	case "logout":
		err = logout(stdout)
	case "help", "-h", "--help":
		fmt.Fprint(stdout, usage)
		return 0
	default:
		fmt.Fprintf(stderr, "unknown command %q\n%s", args[0], usage)
		return 2
	}
	if errors.Is(err, flag.ErrHelp) {
		return 2
	}
	if err != nil {
		fmt.Fprintln(stderr, "helm:", err)
		return 1
	}
	return 0
}

func login(args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("login", flag.ContinueOnError)
	fs.SetOutput(stderr)
	url := fs.String("url", "", "API base URL, e.g. http://127.0.0.1:8300")
	email := fs.String("email", "", "your email")
	keyFile := fs.String("api-key-file", "", "file holding the helm client API key (or set HELM_API_KEY)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *url == "" || *email == "" {
		fs.Usage()
		return flag.ErrHelp
	}
	cfg := config{URL: strings.TrimRight(*url, "/"), APIKeyFile: *keyFile, Email: *email}
	c, err := newClient(cfg)
	if err != nil {
		return err
	}
	password := os.Getenv("HELM_PASSWORD")
	if password == "" {
		if password, err = readPassword(stdin, stderr); err != nil {
			return err
		}
	}
	var s session
	if err := c.call(http.MethodPost, "/auth/signin", map[string]string{"email": *email, "password": password}, http.StatusOK, &s); err != nil {
		return err
	}
	cfg.AccessToken, cfg.RefreshToken = s.AccessToken, s.RefreshToken
	if err := saveConfig(cfg); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "logged in as %s\n", s.User.Email)
	return nil
}

// readPassword prompts on stderr and reads one line, with echo off when stdin is a terminal.
func readPassword(stdin io.Reader, stderr io.Writer) (string, error) {
	fmt.Fprint(stderr, "Password: ")
	if f, ok := stdin.(*os.File); ok {
		if st, err := f.Stat(); err == nil && st.Mode()&os.ModeCharDevice != 0 {
			if stty(f, "-echo") == nil {
				defer func() { _ = stty(f, "echo"); fmt.Fprintln(stderr) }()
			}
		}
	}
	line, err := bufio.NewReader(stdin).ReadString('\n')
	line = strings.TrimRight(line, "\r\n")
	if line == "" {
		return "", errors.New("no password given")
	}
	if err != nil && !errors.Is(err, io.EOF) {
		return "", err
	}
	return line, nil
}

func stty(tty *os.File, arg string) error {
	cmd := exec.Command("stty", arg)
	cmd.Stdin = tty
	return cmd.Run()
}

func loggedIn() (*client, error) {
	cfg, err := loadConfig()
	if err != nil {
		return nil, err
	}
	if cfg.AccessToken == "" {
		return nil, errors.New("not logged in: run helm login")
	}
	return newClient(cfg)
}

func whoami(stdout io.Writer) error {
	c, err := loggedIn()
	if err != nil {
		return err
	}
	var u user
	if err := c.call(http.MethodGet, "/auth/me", nil, http.StatusOK, &u); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "%s (%s) roles: %s\n", u.Email, u.Name, strings.Join(u.Roles, ","))
	return nil
}

func workflows(stdout io.Writer) error {
	c, err := loggedIn()
	if err != nil {
		return err
	}
	var list struct {
		Workflows []workflow `json:"workflows"`
	}
	if err := c.call(http.MethodGet, "/v1/studio/workflows", nil, http.StatusOK, &list); err != nil {
		return err
	}
	for _, w := range list.Workflows {
		fmt.Fprintf(stdout, "%-20s %-8s %s\n", w.ID, w.Env, w.Title)
	}
	return nil
}

// logout revokes the session on the server (best effort) and forgets the tokens.
func logout(stdout io.Writer) error {
	c, err := loggedIn()
	if err != nil {
		return err
	}
	c.cfg.RefreshToken = "" // no refresh just to sign out
	if resp, err := c.do(context.Background(), http.MethodDelete, "/auth/signout", nil, nil); err == nil {
		resp.Body.Close()
	}
	c.cfg.AccessToken = ""
	if err := saveConfig(c.cfg); err != nil {
		return err
	}
	fmt.Fprintln(stdout, "logged out")
	return nil
}
