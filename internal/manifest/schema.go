package manifest

import (
	"bytes"
	"fmt"
	"os"
	"strings"
	"sync"

	"github.com/janishar/helmstudio/schema"
	"github.com/santhosh-tekuri/jsonschema/v5"
)

// schemaURL names the embedded schema inside the compiler; references within
// the schema resolve against it.
const schemaURL = "https://helmstudio.local/schema/manifest.json"

// compiledSchema compiles schema/manifest.json, embedded by package schema so a
// binary built with -trimpath, moved elsewhere, or imported from the module
// cache validates the same way a checkout does (M4 second review #1; this
// replaces M0's source-relative lookup, whose -trimpath and wrong-worktree
// failures docs/decisions.md recorded). HELM_SCHEMA_PATH still overrides it,
// for validating against a schema under development.
func compiledSchema() (*jsonschema.Schema, error) {
	schemaOnce.Do(func() {
		c := jsonschema.NewCompiler()
		// draft 2020-12 treats "format" as an annotation, not an assertion,
		// unless asked — meaning repo: "not a uri at all" would otherwise
		// pass despite the schema declaring "format": "uri". Turning this on
		// does not edit schema/manifest.json, but it does make this validator
		// reject manifests a default 2020-12 validator accepts (repo and
		// license_url are the two fields affected). That is close to a
		// contract change; see docs/decisions.md (M0, "format assertion").
		c.AssertFormat = true
		if p := os.Getenv("HELM_SCHEMA_PATH"); p != "" {
			compiled, compileErr = c.Compile(p)
			return
		}
		if err := c.AddResource(schemaURL, bytes.NewReader(schema.Manifest)); err != nil {
			compileErr = fmt.Errorf("manifest: loading the embedded schema: %w", err)
			return
		}
		compiled, compileErr = c.Compile(schemaURL)
	})
	return compiled, compileErr
}

var (
	schemaOnce sync.Once
	compiled   *jsonschema.Schema
	compileErr error
)

// validateSchema checks doc (as produced by decodeGeneric) against
// schema/manifest.json and flattens the library's nested cause tree into
// leaf-level Errors, each naming the instance location the schema rejected.
func validateSchema(file string, doc any) []Error {
	s, err := compiledSchema()
	if err != nil {
		return []Error{{File: file, Pointer: "/", Rule: "schema", Message: err.Error()}}
	}
	if err := s.Validate(doc); err != nil {
		ve, ok := err.(*jsonschema.ValidationError)
		if !ok {
			return []Error{{File: file, Pointer: "/", Rule: "schema", Message: err.Error()}}
		}
		return flattenSchemaError(file, ve)
	}
	return nil
}

// knownKeywordMessages maps the tail of a schema keyword's location (after
// the "#") to a message written from that keyword's own intent in
// schema/manifest.json, for the handful of not/anyOf/oneOf nodes whose
// library-generated Message ("not failed", "valid against schemas at
// indexes 0 and 1") means nothing without already knowing the schema. A node
// matched here is not recursed into further — its whole point is to replace
// what its Causes would otherwise report as two or three confusing leaves.
var knownKeywordMessages = map[string]string{
	"/allOf/0":                               "declares neither 'repo' nor 'local_path' — a studio is cloned or it is local, never neither",
	"/allOf/1":                               "declares both 'processes' and 'run' — they are mutually exclusive; run is sugar for a single-entry processes[]",
	"/$defs/process/properties/port/not":     "declares both port.prefer and port.fixed — a process listens on one or the other, not both",
	"/$defs/process/properties/health/oneOf": "declares more than one health probe shape (path, tcp, exec) — exactly one is required",
}

func keywordSuffix(absoluteKeywordLocation string) string {
	if i := strings.IndexByte(absoluteKeywordLocation, '#'); i >= 0 {
		return absoluteKeywordLocation[i+1:]
	}
	return absoluteKeywordLocation
}

// pointerFrom normalises the library's InstanceLocation into the "/a/b/c" or
// "/" shape the rest of this package uses. InstanceLocation already carries
// its own leading slash for a non-root location (e.g. "/processes/0/port"),
// so naively prepending another one produces "//processes/0/port".
func pointerFrom(instanceLocation string) string {
	if instanceLocation == "" {
		return "/"
	}
	if strings.HasPrefix(instanceLocation, "/") {
		return instanceLocation
	}
	return "/" + instanceLocation
}

// flattenSchemaError walks the library's Causes tree. At a node matching
// knownKeywordMessages it stops and emits one hand-written error; otherwise
// it recurses to the leaves — the nodes that actually name a keyword
// violation — rather than surfacing the single top-level "does not
// validate" summary, which for a nested allOf/anyOf/oneOf failure names the
// wrong location.
func flattenSchemaError(file string, ve *jsonschema.ValidationError) []Error {
	if msg, ok := knownKeywordMessages[keywordSuffix(ve.AbsoluteKeywordLocation)]; ok {
		return []Error{{
			File:    file,
			Pointer: pointerFrom(ve.InstanceLocation),
			Rule:    "schema",
			Message: msg,
		}}
	}
	if len(ve.Causes) == 0 {
		return []Error{{
			File:    file,
			Pointer: pointerFrom(ve.InstanceLocation),
			Rule:    "schema",
			Message: ve.Message,
		}}
	}
	var out []Error
	for _, c := range ve.Causes {
		out = append(out, flattenSchemaError(file, c)...)
	}
	return out
}
