package main

import (
	"fmt"
	"strconv"
	"strings"
)

// The launcher browser client (docs/decisions.md M6 Q12).
//
// 04 §11 rule 2 says no URL, header or response shape appears anywhere in
// helm-ui-sdk, and rule 3 that a component receives a client rather than
// building one. The launcher's install and process screens need the same
// terminal a studio uses, but install logs and process logs are `launcher`
// operations, which M4 Q1 keeps out of the runtime SDK. So the launcher gets a
// client of its own, generated from the same document by the same generator,
// and the only code that knows the wire is still generated code.
//
// It is written to web/, embedded with the launcher's page and served only
// from the daemon's own origin — never under /sdk/, where a studio's page
// could reach it.
//
// It carries operations only. Transport and the typed error shape come from
// the runtime SDK's own transport, served at /sdk/v1/runtime/transport.js, so
// there is exactly one mapping of status to kind (04 §4) rather than a second
// one that drifts.

const launcherTransport = "/sdk/v1/runtime/transport.js"

func (op *Op) launcher() bool {
	for _, t := range op.Tags {
		if t == "launcher" {
			return true
		}
	}
	return false
}

func launcherGroups(d *Doc) []string {
	var out []string
	seen := map[string]bool{}
	for _, op := range d.Ops {
		if op.launcher() && !seen[op.Group] {
			seen[op.Group] = true
			out = append(out, op.Group)
		}
	}
	return out
}

func launcherOpsOf(d *Doc, group string) []*Op {
	var out []*Op
	for _, op := range d.Ops {
		if op.launcher() && op.Group == group {
			out = append(out, op)
		}
	}
	return out
}

func genLauncher(d *Doc) string {
	var b strings.Builder
	fmt.Fprintf(&b, "// %s\n", header)
	b.WriteString("// The launcher's browser client: the launcher-tagged operations of\n" +
		"// api/openapi.yaml (docs/decisions.md M6 Q12). Served with the launcher's own\n" +
		"// page, never under /sdk/v1/. Method names and semantics are the runtime\n" +
		"// SDK's, and so is the error shape, which comes from its transport.\n\n")
	fmt.Fprintf(&b, "import { HelmError, Transport } from %q;\n\n", launcherTransport)
	b.WriteString("export { HelmError };\n\n")
	fmt.Fprintf(&b, "export const API_VERSION = %q;\n", d.Version)

	for _, grp := range launcherGroups(d) {
		fmt.Fprintf(&b, "\n/** The %s group. */\nexport class %sGroup {\n  constructor(transport) {\n    this.t = transport;\n  }\n", grp, goName(grp))
		for _, op := range launcherOpsOf(d, grp) {
			var args []string
			for _, p := range op.paramsIn("path") {
				args = append(args, camel(p.Name))
			}
			bodyKind := ""
			switch {
			case op.Body == nil:
			case op.BodyType == "application/json" || op.BodyType == "application/merge-patch+json":
				bodyKind = "json"
				args = append(args, "body")
			default:
				bodyKind = "raw"
				args = append(args, "body", "contentType")
			}
			ps := op.paramsIn("query", "header")
			if len(ps) > 0 {
				args = append(args, "params = {}")
			}
			fmt.Fprintf(&b, "\n  /** %s (%s %s) */\n  %s(%s) {\n", strings.ReplaceAll(op.Summary, "*/", "* /"), op.Verb, op.Path, camel(op.Method), strings.Join(args, ", "))
			path := pathTemplate(op, strconv.Quote, func(p string) string { return "encodeURIComponent(" + camel(p) + ")" }, " + ")
			var query, headers []string
			for _, p := range ps {
				entry := fmt.Sprintf("%q: params.%s", p.Name, camel(p.Name))
				if p.In == "header" {
					headers = append(headers, entry)
				} else {
					query = append(query, entry)
				}
			}
			opts := []string{
				"query: {" + strings.Join(query, ", ") + "}",
				"headers: {" + strings.Join(headers, ", ") + "}",
				"expect: " + strconv.Quote(expect(op)),
			}
			switch bodyKind {
			case "json":
				opts = append(opts, "json: body", "contentType: "+strconv.Quote(op.BodyType))
			case "raw":
				opts = append(opts, "raw: body", "contentType")
			}
			fmt.Fprintf(&b, "    return this.t.request(%q, %s, { %s });\n  }\n", op.Verb, path, strings.Join(opts, ", "))
		}
		b.WriteString("}\n")
	}

	b.WriteString("\n/** One property per launcher group. */\nexport class LauncherClient {\n  constructor(transport) {\n")
	for _, grp := range launcherGroups(d) {
		fmt.Fprintf(&b, "    this.%s = new %sGroup(transport);\n", camel(grp), goName(grp))
	}
	b.WriteString("  }\n}\n")
	b.WriteString("\n// connect returns a client for the launcher API on this origin. The launcher\n" +
		"// holds no token: its operations are the ones the daemon serves to its own\n" +
		"// page, guarded by the Host and Origin checks rather than by a bearer token.\n" +
		"export function connect({ base = \"/api/v1\", fetch } = {}) {\n" +
		"  return new LauncherClient(new Transport(base, undefined, { fetch }));\n}\n")
	return b.String()
}
