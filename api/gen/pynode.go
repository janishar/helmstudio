package main

import (
	"fmt"
	"strconv"
	"strings"
)

// pathTemplate renders op.Path with each {param} replaced by conv(param).
func pathTemplate(op *Op, lit func(string) string, param func(string) string, join string) string {
	var parts []string
	rest := op.Path
	for rest != "" {
		i := strings.Index(rest, "{")
		if i < 0 {
			parts = append(parts, lit(rest))
			break
		}
		if i > 0 {
			parts = append(parts, lit(rest[:i]))
		}
		j := strings.Index(rest, "}")
		parts = append(parts, param(rest[i+1:j]))
		rest = rest[j+1:]
	}
	return strings.Join(parts, join)
}

func expect(op *Op) string {
	switch op.kind() {
	case "json":
		return "json"
	case "raw":
		return "raw"
	case "sse":
		return "sse"
	}
	return "empty"
}

func genPython(d *Doc) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# %s\n\"\"\"Generated groups of the helmstudio runtime SDK. Import from helm_runtime_sdk.\"\"\"\n\n", header)
	fmt.Fprintf(&b, "from typing import Any, Dict, Optional, Sequence\n\nfrom ._transport import Transport, quote\n\nAPI_VERSION = %q\n\n", d.Version)
	for _, grp := range groups(d) {
		fmt.Fprintf(&b, "\nclass %sGroup:\n    \"\"\"The %s group.\"\"\"\n\n    def __init__(self, transport: Transport) -> None:\n        self._t = transport\n", goName(grp), grp)
		for _, op := range opsOf(d, grp) {
			var args []string
			for _, p := range op.paramsIn("path") {
				args = append(args, snake(p.Name)+": str")
			}
			bodyKind := ""
			switch {
			case op.Body == nil:
			case op.BodyType == "application/json" || op.BodyType == "application/merge-patch+json":
				bodyKind = "json"
				args = append(args, "body: Dict[str, Any]")
			default:
				bodyKind = "raw"
				args = append(args, "body: bytes", "content_type: str")
			}
			ps := op.paramsIn("query", "header")
			if len(ps) > 0 {
				args = append(args, "*")
				for _, p := range ps {
					t := "Optional[Any]"
					if strings.HasPrefix(newGoTypes(d, "").typeOf(p.S, ""), "[]") {
						t = "Optional[Sequence[str]]"
					}
					if p.Required {
						args = append(args, snake(p.Name)+": Any")
					} else {
						args = append(args, snake(p.Name)+": "+t+" = None")
					}
				}
			}
			fmt.Fprintf(&b, "\n    def %s(self%s) -> Any:\n", snake(op.Method), prefixed(", ", args))
			fmt.Fprintf(&b, "        \"\"\"%s (%s %s)\"\"\"\n", strings.ReplaceAll(op.Summary, `"`, `'`), op.Verb, op.Path)
			path := pathTemplate(op, strconv.Quote, func(p string) string { return "quote(" + snake(p) + ")" }, " + ")
			var query, headers []string
			for _, p := range ps {
				entry := fmt.Sprintf("%q: %s", p.Name, snake(p.Name))
				if p.In == "header" {
					headers = append(headers, entry)
				} else {
					query = append(query, entry)
				}
			}
			call := []string{strconv.Quote(op.Verb), path,
				"query={" + strings.Join(query, ", ") + "}",
				"headers={" + strings.Join(headers, ", ") + "}",
				"expect=" + strconv.Quote(expect(op))}
			switch bodyKind {
			case "json":
				call = append(call, "json_body=body", "content_type="+strconv.Quote(op.BodyType))
			case "raw":
				call = append(call, "raw_body=body", "content_type=content_type")
			}
			fmt.Fprintf(&b, "        return self._t.request(%s)\n", strings.Join(call, ", "))
		}
	}
	b.WriteString("\n\nclass Client:\n    \"\"\"One attribute per API group, the same names as the Go and Node clients.\"\"\"\n\n    def __init__(self, transport: Transport) -> None:\n")
	for _, grp := range groups(d) {
		fmt.Fprintf(&b, "        self.%s = %sGroup(transport)\n", snake(grp), goName(grp))
	}
	return b.String()
}

func prefixed(sep string, args []string) string {
	if len(args) == 0 {
		return ""
	}
	return sep + strings.Join(args, ", ")
}

func genNode(d *Doc) string {
	var b strings.Builder
	fmt.Fprintf(&b, "// %s\n// Generated groups of the helmstudio runtime SDK. Import from ./index.js.\n\n", header)
	fmt.Fprintf(&b, "export const API_VERSION = %q;\n", d.Version)
	for _, grp := range groups(d) {
		fmt.Fprintf(&b, "\n/** The %s group. */\nexport class %sGroup {\n  constructor(transport) {\n    this.t = transport;\n  }\n", grp, goName(grp))
		for _, op := range opsOf(d, grp) {
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
	b.WriteString("\n/** One property per API group, the same names as the Go and Python clients. */\nexport class Client {\n  constructor(transport) {\n")
	for _, grp := range groups(d) {
		fmt.Fprintf(&b, "    this.%s = new %sGroup(transport);\n", camel(grp), goName(grp))
	}
	b.WriteString("  }\n}\n")
	return b.String()
}
