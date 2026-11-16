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
  helm run <workflow_id> [--session <id|new>] [-p node.param=value ...] [--lora <id>:<strength> ...] [-o|--out <dir>]
             saves each output (a PNG, or a video's MP4) as <dir>/<run_id>_<n>.<ext>
             --lora fills the rows of the workflow's LoRA stack, in order
             [--image <take|file>] [--keyframe <frame>:<take|file> ...] [--duration <frames>|auto]   a video's first
             frame and keyframes (files are uploaded first); auto: the model picks the length (printed when it ends)
  helm batch <workflow_id> (--lines <file|-> [--param node.param] | --csv <file> | --grid node.param=a,b,c ...)
             [-p node.param=value ...] [--seeds N] [--session <id>] [--title <t>]   exit 1 if any row failed
  helm batch --pipeline <pipeline_id> (--lines ... [--param inputs.name] | --csv ... | --grid inputs.name=a,b ...) [-p inputs.name=value ...]
             the pipeline's inputs are the columns; --lines sets its first Text or Image input by default
  helm batch review <batch_id>        rows waiting at a gate, with their candidate takes
  helm batch pick <batch_id> <row> (<take_id>... | --reject) [--note text]   answer the gate (or Suggest judge) the
             row waits at; review shows the judge's suggestion; --note (a judge) becomes an example in its memory
  helm batch decisions <batch_id> [row]   judge, write and decide decisions (verdicts, reasons, routes, replays)
  helm batch chat <batch_id> [text ...]   the director's chat (state, messages, its tool calls); with a text, say it
  helm batches [--limit n] [--cursor c]
  helm batch-stop <batch_id>
  helm takes [--kind image|video|audio] [--source create|upload] [--starred] [--session <id>] [--q <text>] [--limit n] [--cursor c]
  helm timelines                      your sequences (id, revision, V1 clips, length, size and rate, updated, name)
  helm timeline show <id>             a sequence's tracks and clips (gaps, offline clips, dissolves, shots)
  helm timeline add <id> [--track A2] <take_id>...   "Add to timeline": append takes in order (A2…: a video's sound)
  helm timeline export <id> [-o file.mp4] [--plan] [--host h]   --plan: copy or re-encode and why, exit 1 if blocked;
             else export the current revision, follow it (Ctrl-C cancels) and save the MP4
  helm upload <file>                  a png, jpeg or webp image, into the gallery
  helm use [--param node.param] <take_id> <workflow_id>   print the helm run command with the take as reference 1

An image input takes a comma list of take ids and @files (uploaded first): -p encode.refs=@a.png,<take_id>
  helm workflows [--media image|video]   templates, then your own (id, source, version, env, media, title)
  helm workflow get <id> > file.json  a workflow as a file (yours: with id and version; a template: based_on)
  helm workflow save file.json        replace yours (id + version; stale = 409) or create one; prints problems
  helm workflow validate file.json    print every problem; exit 1 if it cannot run
  helm workflow copy <id> [--title t] a copy of a template or of yours; prints the new id
  helm workflow rm <id>
  helm pipelines                      your pipelines (id, version, last batch's status, title)
  helm pipeline get|save|validate|copy|rm ...   as helm workflow, for a pipeline file (title, nodes, edges)
  helm memory list --pipeline <id> [--kind judge_example|note]   a pipeline's memory (id, kind, key, source, text)
  helm memory add --pipeline <id> [--key k] <text>   a note its next batches read; helm memory rm <item_id>
  helm elements [--q prefix] [--group character|location|prop]   your elements (id, group, @handle, images, version, name)
  helm element get <id> > file.json   an element as a file (id, version, group, name, handle, text, images as take ids)
  helm element save file.json         replace yours (id + version; stale = 409) or, without an id, make one
  helm element rm <id>

A prompt may mention your elements as @handle (@@ is a literal @): helm sends it as typed, the server expands it.
  helm looks [--group film|camera|colour|lighting] [--source mine|starter] [--workflow id]   looks (id, source, group, needs, version, name)
  helm look get <id> > file.json      a look as a file (yours: id, version; a starter look: based_on, saved as your copy)
  helm look save file.json            replace yours or make one; "from_take": {"take_id": ...} fills text and LoRAs from a take
  helm look rm <id>
  helm run|batch ... --look <id>      run with a look (repeatable; with batch: every row)
  helm loras [--source shared|mine] [--family f] [--q words]   LoRAs you can use (id, source, state, family, rank, size, name)
  helm lora upload <file.safetensors>   your own LoRA: uploaded, checked and indexed
  helm lora rm <id>
  helm providers                      (admin) local servers and remote providers; remote models on or off
  helm providers set <name> file.json | rm <name> | test <name> [--model m]   a ProviderPut file; keys are never sent
  helm hosts                          each host's shared memory (used / budget), its envs and what each holds
  helm hosts pin|unpin <host> <env> <component> <model> <variant> <precision>   (admin) keep it loaded
  helm hosts unload <host> (--all | --cached <env> | <env> <component> <model> <variant> <precision>)   (admin)
  helm hosts after-run <host> unload|keep_warm|keep_pinned [--idle-min n] [--reserve-gb x]   (admin) every env of it
  helm models                         every model variant: precisions, size, state per host
  helm model download <model> --host <h> [--variant v] [--accept-licence]   (admin) a queued job; prints the job
  helm model rm <model> --host <h> [--variant v]   (admin) delete its files there (no variant: every one)
  helm keys list | new <client> | revoke <id>   (admin) application API keys; a new key is shown once
  helm import <path> --user <email> [--dry-run] [--limit n]   (admin) an old helmstudio gallery (~/.helmstudio,
             read only) into the user's gallery; idempotent; --dry-run only counts
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
	case "batch":
		err = batchCmd(args[1:], stdin, stdout, stderr, stop)
	case "batches":
		err = batchesCmd(args[1:], stdout, stderr)
	case "batch-stop":
		err = batchStop(args[1:], stdout)
	case "takes":
		err = takesCmd(args[1:], stdout, stderr)
	case "timelines":
		err = timelinesCmd(stdout)
	case "timeline":
		err = timelineCmd(args[1:], stdout, stderr, stop)
	case "upload":
		err = uploadCmd(args[1:], stdout)
	case "use":
		err = useCmd(args[1:], stdout, stderr)
	case "workflows":
		err = workflows(args[1:], stdout, stderr)
	case "workflow":
		err = docCmd(workflowKind, args[1:], stdout, stderr)
	case "pipelines":
		err = pipelines(stdout)
	case "pipeline":
		err = docCmd(pipelineKind, args[1:], stdout, stderr)
	case "memory":
		err = memoryCmd(args[1:], stdout, stderr)
	case "elements":
		err = elements(args[1:], stdout, stderr)
	case "element":
		err = docCmd(elementKind, args[1:], stdout, stderr)
	case "looks":
		err = looks(args[1:], stdout, stderr)
	case "look":
		err = docCmd(lookKind, args[1:], stdout, stderr)
	case "loras":
		err = lorasCmd(args[1:], stdout, stderr)
	case "lora":
		err = loraCmd(args[1:], stdout)
	case "providers":
		err = providersCmd(args[1:], stdout, stderr)
	case "hosts":
		if len(args) == 1 {
			err = hostsCmd(stdout)
		} else {
			err = hostsAdminCmd(args[1:], stdout, stderr)
		}
	case "models":
		err = modelsCmd(stdout)
	case "model":
		err = modelCmd(args[1:], stdout, stderr)
	case "keys":
		err = keysCmd(args[1:], stdout)
	case "import":
		err = importCmd(args[1:], stdout, stderr)
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
	var ee *exitErr
	if errors.As(err, &ee) {
		if ee.err != nil {
			fmt.Fprintln(stderr, "helm:", ee.err)
		}
		return ee.code
	}
	if err != nil {
		fmt.Fprintln(stderr, "helm:", err)
		return 1
	}
	return 0
}

// exitErr ends helm with its code (and its message, when it has one).
type exitErr struct {
	code int
	err  error
}

func (e *exitErr) Error() string { return fmt.Sprint(e.err) }

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
