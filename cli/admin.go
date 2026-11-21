package main

// Models, model jobs, the Hosts admin and API keys (API 1.16; contracts/README.md, Models and model jobs, Hosts admin).

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

type modelList struct {
	Licences []struct {
		ID    string  `json:"id"`
		Title string  `json:"title"`
		URL   *string `json:"url"`
		Text  string  `json:"text"`
	} `json:"licences"`
	Models []struct {
		ID       string `json:"id"`
		Title    string `json:"title"`
		Variants []struct {
			ID         string   `json:"id"`
			Title      string   `json:"title"`
			Precisions []string `json:"precisions"`
			Licence    *struct {
				ID         string `json:"id"`
				MustAccept bool   `json:"must_accept"`
			} `json:"licence"`
			Gated         bool   `json:"gated"`
			DownloadBytes *int64 `json:"download_bytes"`
			Hosts         []struct {
				Host     string   `json:"host"`
				State    string   `json:"state"`
				Mismatch []string `json:"mismatch"`
				Job      *struct {
					DoneBytes  int64  `json:"done_bytes"`
					TotalBytes *int64 `json:"total_bytes"`
				} `json:"job"`
			} `json:"hosts"`
		} `json:"variants"`
	} `json:"models"`
}

// modelsCmd prints each model variant with its size and its state on every host.
func modelsCmd(stdout io.Writer) error {
	c, err := loggedIn()
	if err != nil {
		return err
	}
	var l modelList
	if err := c.call(http.MethodGet, "/v1/studio/models", nil, http.StatusOK, &l); err != nil {
		return err
	}
	for _, m := range l.Models {
		for _, v := range m.Variants {
			size := "-"
			if v.DownloadBytes != nil {
				size = fmt.Sprintf("%.1f GB", float64(*v.DownloadBytes)/1e9)
			}
			s := fmt.Sprintf("%-16s %-10s %-14s %9s", m.ID, v.ID, strings.Join(v.Precisions, "|"), size)
			for _, h := range v.Hosts {
				state := h.State
				switch {
				case h.Job != nil && h.Job.TotalBytes != nil && *h.Job.TotalBytes > 0:
					state += fmt.Sprintf(" %d%%", h.Job.DoneBytes*100 / *h.Job.TotalBytes)
				case len(h.Mismatch) > 0:
					state += fmt.Sprintf(" (%d files)", len(h.Mismatch))
				}
				s += "  " + h.Host + ": " + state
			}
			if v.Gated {
				s += "  gated"
			}
			fmt.Fprintln(stdout, s)
		}
	}
	return nil
}

type modelJob struct {
	ID      string `json:"id"`
	Kind    string `json:"kind"`
	Host    string `json:"host"`
	Env     string `json:"env"`
	Title   string `json:"title"`
	State   string `json:"state"`
	Failure *struct {
		Class  string `json:"class"`
		Detail string `json:"detail"`
	} `json:"failure"`
	Keeps []struct {
		Component   string   `json:"component"`
		FromVariant string   `json:"from_variant"`
		For         []string `json:"for"`
	} `json:"keeps"`
}

func (j modelJob) print(stdout io.Writer) {
	fmt.Fprintf(stdout, "%s %s %s %s on %s.%s\n", j.ID, j.Kind, j.State, j.Title, j.Host, j.Env)
	for _, k := range j.Keeps {
		fmt.Fprintf(stdout, "  keeps %s of %s (for %s)\n", k.Component, k.FromVariant, strings.Join(k.For, ", "))
	}
	if j.Failure != nil {
		fmt.Fprintf(stdout, "  failed %s: %s\n", j.Failure.Class, j.Failure.Detail)
	}
}

// callJob sends a model-job request: 202 a new job, 200 the same unfinished one.
func (c *client) callJob(method, path string, in any, stdout io.Writer) error {
	resp, err := c.do(context.Background(), method, path, in, nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusAccepted {
		return readError(resp)
	}
	var j modelJob
	if err := json.NewDecoder(resp.Body).Decode(&j); err != nil {
		return err
	}
	j.print(stdout)
	return nil
}

// modelCmd: helm model download|rm <model> --host <h> [--variant v] [--accept-licence].
func modelCmd(args []string, stdout, stderr io.Writer) error {
	const use = "usage: helm model download <model> --host <h> [--variant v] [--accept-licence] | helm model rm <model> --host <h> [--variant v]"
	if len(args) < 2 || (args[0] != "download" && args[0] != "rm") {
		return errors.New(use)
	}
	fs := flag.NewFlagSet("model", flag.ContinueOnError)
	fs.SetOutput(stderr)
	host := fs.String("host", "", "the host")
	variant := fs.String("variant", "", "the variant (download: default the first; rm: default every one)")
	accept := fs.Bool("accept-licence", false, "accept the variant's licence (download)")
	if err := fs.Parse(args[2:]); err != nil {
		return err
	}
	if *host == "" || fs.NArg() > 0 {
		return errors.New(use)
	}
	c, err := loggedIn()
	if err != nil {
		return err
	}
	model := args[1]
	if args[0] == "rm" {
		q := url.Values{"host": {*host}}
		if *variant != "" {
			q.Set("variant", *variant)
		}
		return c.callJob(http.MethodDelete, "/v1/studio/admin/models/"+url.PathEscape(model)+"?"+q.Encode(), nil, stdout)
	}
	if !*accept {
		if text, id := c.licenceToAccept(model, *variant); text != "" {
			fmt.Fprintln(stdout, text)
			return fmt.Errorf("licence %s must be accepted: read it above, then pass --accept-licence", id)
		}
	}
	body := map[string]any{"host": *host}
	if *variant != "" {
		body["variant"] = *variant
	}
	if *accept {
		body["accept_licence"] = true
	}
	return c.callJob(http.MethodPost, "/v1/studio/admin/models/"+url.PathEscape(model)+"/download", body, stdout)
}

// licenceToAccept is the text and id of the variant's licence when it must be accepted ("" otherwise, or when the
// list cannot be read: the server then refuses with its own 422).
func (c *client) licenceToAccept(model, variant string) (text, id string) {
	var l modelList
	if c.call(http.MethodGet, "/v1/studio/models", nil, http.StatusOK, &l) != nil {
		return "", ""
	}
	for _, m := range l.Models {
		for i, v := range m.Variants {
			if m.ID != model || (variant != "" && v.ID != variant) || (variant == "" && i > 0) {
				continue
			}
			if v.Licence == nil || !v.Licence.MustAccept {
				return "", ""
			}
			for _, t := range l.Licences {
				if t.ID == v.Licence.ID {
					return t.Title + "\n\n" + t.Text, t.ID
				}
			}
			return "(licence " + v.Licence.ID + ")", v.Licence.ID
		}
	}
	return "", ""
}

type pinRef struct {
	Env       string `json:"env"`
	Component string `json:"component"`
	Model     string `json:"model"`
	Variant   string `json:"variant"`
	Precision string `json:"precision"`
	Loras     []struct {
		LoraID   string  `json:"lora_id"`
		Strength float64 `json:"strength"`
	} `json:"loras,omitempty"`
}

func (p pinRef) same(q pinRef) bool {
	return p.Env == q.Env && p.Component == q.Component && p.Model == q.Model && p.Variant == q.Variant && p.Precision == q.Precision
}

type hostSettings struct {
	Version       int      `json:"version"`
	AfterRun      string   `json:"after_run"`
	IdleUnloadMin *int     `json:"idle_unload_min"`
	ReserveGB     *float64 `json:"reserve_gb"`
	Pins          []pinRef `json:"pins"`
	Applied       bool     `json:"applied"`
}

// adminOf is the host's admin view (settings, else the runners' policy) from GET /v1/studio/hosts.
func (c *client) adminOf(host string) (settings *hostSettings, policy *hostSettings, err error) {
	var list struct {
		Hosts []struct {
			Host  string `json:"host"`
			Admin *struct {
				Settings *hostSettings `json:"settings"`
				Policy   *hostSettings `json:"policy"`
			} `json:"admin"`
		} `json:"hosts"`
	}
	if err := c.call(http.MethodGet, "/v1/studio/hosts", nil, http.StatusOK, &list); err != nil {
		return nil, nil, err
	}
	found := false
	for _, h := range list.Hosts {
		if h.Host != host {
			continue
		}
		found = true
		if h.Admin != nil {
			if h.Admin.Settings != nil {
				settings = h.Admin.Settings
			}
			if h.Admin.Policy != nil && policy == nil {
				policy = h.Admin.Policy
			}
		}
	}
	if !found {
		return nil, nil, fmt.Errorf("no host %s", host)
	}
	return settings, policy, nil
}

const hostsUse = `usage: helm hosts
  helm hosts pin|unpin <host> <env> <component> <model> <variant> <precision>
  helm hosts unload <host> (--all | --cached <env> | <env> <component> <model> <variant> <precision>)
  helm hosts after-run <host> unload|keep_warm|keep_pinned [--idle-min n] [--reserve-gb x]`

// hostsAdminCmd: pin, unpin, unload and after-run (admin).
func hostsAdminCmd(args []string, stdout, stderr io.Writer) error {
	if len(args) < 2 {
		return errors.New(hostsUse)
	}
	fs := flag.NewFlagSet("hosts", flag.ContinueOnError)
	fs.SetOutput(stderr)
	all := fs.Bool("all", false, "unload: everything on every env, pins included")
	cached := fs.String("cached", "", "unload: the env's cached handles only")
	idle := fs.Int("idle-min", -1, "after-run: minutes idle before keep_warm unloads (0 = never)")
	reserve := fs.Float64("reserve-gb", -1, "after-run: memory kept free on the host")
	rest, err := parseMixed(fs, args[2:])
	if err != nil {
		return err
	}
	host := args[1]
	ref := func() (pinRef, error) {
		if len(rest) != 5 {
			return pinRef{}, errors.New(hostsUse)
		}
		return pinRef{Env: rest[0], Component: rest[1], Model: rest[2], Variant: rest[3], Precision: rest[4]}, nil
	}
	c, err := loggedIn()
	if err != nil {
		return err
	}
	hostPath := "/v1/studio/admin/hosts/" + url.PathEscape(host)
	switch args[0] {
	case "pin", "unpin":
		p, err := ref()
		if err != nil {
			return err
		}
		settings, _, err := c.adminOf(host)
		if err != nil {
			return err
		}
		pins := []pinRef{}
		if settings != nil {
			for _, q := range settings.Pins {
				if !q.same(p) || len(q.Loras) > 0 {
					pins = append(pins, q)
				}
			}
			if args[0] == "unpin" && len(pins) == len(settings.Pins) {
				return fmt.Errorf("%s %s is not pinned on %s", p.Component, p.Model, host)
			}
		} else if args[0] == "unpin" {
			return fmt.Errorf("%s %s is not pinned on %s", p.Component, p.Model, host)
		}
		if args[0] == "pin" {
			pins = append(pins, p)
		}
		var s hostSettings
		if err := c.call(http.MethodPut, hostPath+"/pins", map[string]any{"pins": pins}, http.StatusOK, &s); err != nil {
			return err
		}
		fmt.Fprintf(stdout, "%s: %d pins, version %d\n", host, len(s.Pins), s.Version)
		return nil
	case "unload":
		var body map[string]any
		switch {
		case *all && *cached == "" && len(rest) == 0:
			body = map[string]any{"all": true}
		case !*all && *cached != "" && len(rest) == 0:
			body = map[string]any{"cached_env": *cached}
		case !*all && *cached == "":
			p, err := ref()
			if err != nil {
				return err
			}
			body = map[string]any{"entries": []pinRef{p}}
		default:
			return errors.New(hostsUse)
		}
		var acc struct {
			Requests []struct {
				Env   string `json:"env"`
				State string `json:"state"`
			} `json:"requests"`
		}
		if err := c.call(http.MethodPost, hostPath+"/unload", body, http.StatusAccepted, &acc); err != nil {
			return err
		}
		for _, r := range acc.Requests {
			fmt.Fprintf(stdout, "%s.%s %s\n", host, r.Env, r.State)
		}
		return nil
	case "after-run":
		if len(rest) != 1 {
			return errors.New(hostsUse)
		}
		settings, policy, err := c.adminOf(host)
		if err != nil {
			return err
		}
		if settings == nil {
			settings = policy
		}
		var idleV *int
		var resV *float64
		if settings != nil {
			idleV, resV = settings.IdleUnloadMin, settings.ReserveGB
		}
		if *idle >= 0 {
			idleV = idle
		}
		if *reserve >= 0 {
			resV = reserve
		}
		if idleV == nil || resV == nil {
			return fmt.Errorf("%s reports no idle timeout or memory reserve yet: pass --idle-min and --reserve-gb", host)
		}
		body := map[string]any{"after_run": rest[0], "idle_unload_min": *idleV, "reserve_gb": *resV}
		var s hostSettings
		if err := c.call(http.MethodPut, hostPath+"/settings", body, http.StatusOK, &s); err != nil {
			return err
		}
		fmt.Fprintf(stdout, "%s: after-run %s, idle %d min, reserve %s GB, version %d\n", host, s.AfterRun,
			deref0(s.IdleUnloadMin), strconv.FormatFloat(deref0f(s.ReserveGB), 'f', -1, 64), s.Version)
		return nil
	}
	return errors.New(hostsUse)
}

// parseMixed parses flags placed before, between or after the positional arguments, and returns those.
func parseMixed(fs *flag.FlagSet, args []string) ([]string, error) {
	var pos []string
	for {
		if err := fs.Parse(args); err != nil {
			return nil, err
		}
		if fs.NArg() == 0 {
			return pos, nil
		}
		pos, args = append(pos, fs.Arg(0)), fs.Args()[1:]
	}
}

func deref0(p *int) int {
	if p == nil {
		return 0
	}
	return *p
}

func deref0f(p *float64) float64 {
	if p == nil {
		return 0
	}
	return *p
}

// keysCmd: helm keys list | new <client> | revoke <id> (admin).
func keysCmd(args []string, stdout io.Writer) error {
	const use = "usage: helm keys list | helm keys new <client> | helm keys revoke <id>"
	if len(args) == 0 || (args[0] == "list") != (len(args) == 1) || len(args) > 2 {
		return errors.New(use)
	}
	c, err := loggedIn()
	if err != nil {
		return err
	}
	type apiKey struct {
		ID         string  `json:"id"`
		Client     string  `json:"client"`
		Prefix     string  `json:"prefix"`
		CreatedAt  string  `json:"created_at"`
		RevokedAt  *string `json:"revoked_at"`
		LastUsedAt *string `json:"last_used_at"`
		Key        string  `json:"key"`
	}
	switch args[0] {
	case "list":
		var l struct {
			Keys []apiKey `json:"api_keys"`
		}
		if err := c.call(http.MethodGet, "/auth/admin/apikeys", nil, http.StatusOK, &l); err != nil {
			return err
		}
		for _, k := range l.Keys {
			state, used := "active", "never"
			if k.RevokedAt != nil {
				state = "revoked " + *k.RevokedAt
			}
			if k.LastUsedAt != nil {
				used = *k.LastUsedAt
			}
			fmt.Fprintf(stdout, "%s %-12s %-16s %s used %s  %s\n", k.ID, k.Prefix, k.Client, k.CreatedAt, used, state)
		}
		return nil
	case "new":
		var k apiKey
		if err := c.call(http.MethodPost, "/auth/admin/apikeys", map[string]string{"client": args[1]}, http.StatusCreated, &k); err != nil {
			return err
		}
		fmt.Fprintf(stdout, "%s %s\n%s\nshown once: store it now\n", k.ID, k.Client, k.Key)
		return nil
	case "revoke":
		if err := c.call(http.MethodDelete, "/auth/admin/apikeys/"+url.PathEscape(args[1]), nil, http.StatusNoContent, nil); err != nil {
			return err
		}
		fmt.Fprintln(stdout, "revoked", args[1])
		return nil
	}
	return errors.New(use)
}

// modelPick: helm model pick --host <h> [--all-missing]. Lists the variants the host publishes, asks on stdin which to
// download, and asks for each licence that must be accepted: a licence is only accepted on a typed "y".
func modelPick(args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	const use = "usage: helm model pick --host <h> [--all-missing]"
	fs := flag.NewFlagSet("model pick", flag.ContinueOnError)
	fs.SetOutput(stderr)
	host := fs.String("host", "", "the host")
	allMissing := fs.Bool("all-missing", false, "pick every missing or partial variant")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *host == "" || fs.NArg() > 0 {
		return errors.New(use)
	}
	c, err := loggedIn()
	if err != nil {
		return err
	}
	var l modelList
	if err := c.call(http.MethodGet, "/v1/studio/models", nil, http.StatusOK, &l); err != nil {
		return err
	}
	type row struct{ model, variant, title, state, licence string }
	var rows []row
	for _, m := range l.Models {
		for _, v := range m.Variants {
			for _, h := range v.Hosts {
				if h.Host != *host {
					continue
				}
				r := row{model: m.ID, variant: v.ID, title: m.Title + " " + v.Title, state: h.State}
				size, lic := "-", "-"
				if v.DownloadBytes != nil {
					size = fmt.Sprintf("%.1f GB", float64(*v.DownloadBytes)/1e9)
				}
				if v.Licence != nil {
					lic = v.Licence.ID
					if v.Licence.MustAccept {
						r.licence = v.Licence.ID
						lic += " (accept needed)"
					}
				}
				rows = append(rows, r)
				fmt.Fprintf(stdout, "%3d  %-16s %-28s %-14s %9s  %-30s %s\n", len(rows), m.ID, r.title,
					strings.Join(v.Precisions, "|"), size, lic, h.State)
			}
		}
	}
	if len(rows) == 0 {
		return fmt.Errorf("host %s publishes no models", *host)
	}
	if stdin == nil {
		stdin = strings.NewReader("")
	}
	in := bufio.NewReader(stdin)
	ask := func(prompt string) string {
		fmt.Fprint(stderr, prompt)
		line, _ := in.ReadString('\n') // EOF = an empty answer
		return strings.TrimSpace(line)
	}
	var picked []int
	if *allMissing {
		for i, r := range rows {
			if r.state == "missing" || r.state == "partial" {
				picked = append(picked, i+1)
			}
		}
	} else if picked, err = parseNumbers(ask("Numbers to download (e.g. 1,3,5-7; empty = none): "), len(rows)); err != nil {
		return err
	}
	if len(picked) == 0 {
		fmt.Fprintln(stdout, "nothing picked; nothing queued")
		return nil
	}
	for _, n := range picked {
		r := rows[n-1]
		if r.state == "present" || r.state == "downloading" {
			fmt.Fprintf(stdout, "%d %s %s: already %s, skipped\n", n, r.model, r.variant, r.state)
			continue
		}
		body := map[string]any{"host": *host, "variant": r.variant}
		if r.licence != "" {
			title, link := r.licence, "(no URL; text: helm models)"
			for _, t := range l.Licences {
				if t.ID == r.licence {
					title = t.Title
					if t.URL != nil {
						link = *t.URL
					}
				}
			}
			fmt.Fprintf(stderr, "%s %s needs the licence %s: %s\n", r.model, r.variant, title, link)
			if ask("Accept "+title+"? [y/N] ") != "y" {
				fmt.Fprintf(stdout, "%d %s %s: licence not accepted, skipped\n", n, r.model, r.variant)
				continue
			}
			body["accept_licence"] = true
		}
		if err := c.callJob(http.MethodPost, "/v1/studio/admin/models/"+url.PathEscape(r.model)+"/download", body, stdout); err != nil {
			return err
		}
	}
	return nil
}

// parseNumbers reads "1,3,5-7" (1..max, each once, in the order given); empty = none.
func parseNumbers(s string, max int) ([]int, error) {
	var out []int
	seen := map[int]bool{}
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		lo, hi, isRange := strings.Cut(part, "-")
		a, err1 := strconv.Atoi(strings.TrimSpace(lo))
		b, err2 := a, error(nil)
		if isRange {
			b, err2 = strconv.Atoi(strings.TrimSpace(hi))
		}
		if err1 != nil || err2 != nil || a < 1 || b > max || a > b {
			return nil, fmt.Errorf("bad number %q: use 1-%d, e.g. 1,3,5-7", part, max)
		}
		for i := a; i <= b; i++ {
			if !seen[i] {
				seen[i] = true
				out = append(out, i)
			}
		}
	}
	return out, nil
}
