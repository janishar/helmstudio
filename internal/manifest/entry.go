package manifest

import (
	"bytes"
	"fmt"
	"os"
	"sync"

	"github.com/janishar/helmstudio/schema"
	"github.com/santhosh-tekuri/jsonschema/v5"
	"gopkg.in/yaml.v3"
)

// The registry entry: a pointer at a studio's repository, with an optional
// inline manifest for a repository that ships none yet (docs/decisions.md M7
// Q4; schema/registry-entry.json).
//
// Two documents can arrive at this package now, and a caller usually does not
// know which it has — text pasted into the import dialog is whatever the user
// pasted. So Kind decides by shape, and the one validation path validates
// whichever it is.

// Kind is which of the two documents some text is.
type Kind string

const (
	// KindManifest is a studio's own helmstudio.yaml.
	KindManifest Kind = "manifest"
	// KindPointer is a registry entry: id, repo, ref, and maybe a manifest.
	KindPointer Kind = "pointer"
	// KindUnknown is text that is neither, or is not YAML at all.
	KindUnknown Kind = "unknown"
)

// Entry is a registry entry as stored in studios/<id>.yaml.
type Entry struct {
	SchemaVersion int       `yaml:"schema_version,omitempty" json:"schema_version,omitempty"`
	ID            string    `yaml:"id" json:"id"`
	Repo          string    `yaml:"repo" json:"repo"`
	Ref           string    `yaml:"ref" json:"ref"`
	Manifest      *Manifest `yaml:"manifest,omitempty" json:"manifest,omitempty"`

	// Digest is sha256 over the bytes the entry was read from.
	Digest string `yaml:"-" json:"digest,omitempty"`
}

const entrySchemaURL = "https://helmstudio.local/schema/registry-entry.json"

var (
	entryOnce sync.Once
	entrySch  *jsonschema.Schema
	entryErr  error
)

// compiledEntrySchema compiles schema/registry-entry.json with
// schema/manifest.json available to it, since the inline manifest is a $ref
// across the two documents.
func compiledEntrySchema() (*jsonschema.Schema, error) {
	entryOnce.Do(func() {
		c := jsonschema.NewCompiler()
		c.AssertFormat = true
		// The inline manifest refers to the manifest schema by its published
		// $id, so both documents go in under the ids they claim.
		manifestID := "https://helmstudio.in/schema/manifest-1.json"
		manifestSrc := schema.Manifest
		if p := os.Getenv("HELM_SCHEMA_PATH"); p != "" {
			b, err := os.ReadFile(p)
			if err != nil {
				entryErr = fmt.Errorf("manifest: reading %s: %w", p, err)
				return
			}
			manifestSrc = b
		}
		if err := c.AddResource(manifestID, bytes.NewReader(manifestSrc)); err != nil {
			entryErr = fmt.Errorf("manifest: loading the embedded manifest schema: %w", err)
			return
		}
		if err := c.AddResource(entrySchemaURL, bytes.NewReader(schema.RegistryEntry)); err != nil {
			entryErr = fmt.Errorf("manifest: loading the embedded registry-entry schema: %w", err)
			return
		}
		entrySch, entryErr = c.Compile(entrySchemaURL)
	})
	return entrySch, entryErr
}

// DetectKind reports which document data is, by shape rather than by filename.
//
// A registry entry has `repo` and `ref` at the top and none of a manifest's
// required fields; a manifest has `name`, `kinds`, `requires`, `runtime` or
// `processes`. A pointer that also carries `manifest:` is still a pointer. The
// test is deliberately about what is *there* rather than what is absent, so a
// half-written manifest is reported as an invalid manifest rather than as a
// malformed pointer — the errors a person needs are the ones for the document
// they were trying to write.
func DetectKind(data []byte) Kind {
	var top map[string]any
	if err := yaml.Unmarshal(data, &top); err != nil || top == nil {
		return KindUnknown
	}
	if _, ok := top["manifest"]; ok {
		return KindPointer
	}
	for _, k := range []string{"name", "kinds", "requires", "runtime", "processes", "run", "build", "weights"} {
		if _, ok := top[k]; ok {
			return KindManifest
		}
	}
	_, hasRepo := top["repo"]
	_, hasRef := top["ref"]
	if hasRepo && hasRef {
		return KindPointer
	}
	return KindManifest
}

// ValidateEntryBytes validates a registry entry: the envelope, then the inline
// manifest if there is one, then the rule JSON Schema cannot express — that
// where the inline manifest sets id, repo or ref, each equals the pointer's.
//
// Two copies of a fact drift. The inline manifest exists only for a repository
// that ships none, and the day it ships one the pull request that moves `ref`
// deletes the inline copy; until then, the two must agree about which studio
// and which repository they describe.
func ValidateEntryBytes(name string, data []byte) (Result, error) {
	file := name
	if err := rejectMultiDocument(data); err != nil {
		return Result{File: file, Errors: []Error{{
			File: file, Pointer: "/", Rule: "parse", Message: err.Error(),
		}}}, nil
	}
	var root yaml.Node
	_ = yaml.Unmarshal(data, &root)

	doc, err := decodeGeneric(data)
	if err != nil {
		return Result{File: file, Errors: []Error{{
			File: file, Pointer: "/", Rule: "parse", Message: err.Error(),
		}}}, nil
	}

	s, err := compiledEntrySchema()
	if err != nil {
		return Result{File: file, Errors: []Error{{File: file, Pointer: "/", Rule: "schema", Message: err.Error()}}}, nil
	}
	if err := s.Validate(doc); err != nil {
		ve, ok := err.(*jsonschema.ValidationError)
		if !ok {
			return Result{File: file, Errors: []Error{{File: file, Pointer: "/", Rule: "schema", Message: err.Error()}}}, nil
		}
		errs := flattenSchemaError(file, ve)
		attachLines(&root, errs)
		return Result{File: file, Errors: errs}, nil
	}

	var e Entry
	if err := yaml.Unmarshal(data, &e); err != nil {
		return Result{}, fmt.Errorf("registry entry %s passed schema validation but failed typed decode: %w", file, err)
	}

	var errs []Error
	if e.Manifest != nil {
		// The inline manifest is held to every rule a standalone one is: the
		// semantic rules run over it exactly as they would over the file in
		// the studio's own repository, because that is what it stands in for.
		errs = append(errs, prefixPointers("/manifest", validateSemantic(file, e.Manifest))...)
		errs = append(errs, entryAgreement(file, &e)...)
	}
	attachLines(&root, errs)
	if errs == nil {
		errs = []Error{}
	}
	return Result{File: file, Errors: errs}, nil
}

// entryAgreement is the field-equality rule: where the inline manifest sets a
// field the pointer also sets, the two must be equal.
func entryAgreement(file string, e *Entry) []Error {
	var errs []Error
	check := func(field, pointerValue, inlineValue string) {
		if inlineValue == "" || pointerValue == "" || inlineValue == pointerValue {
			return
		}
		errs = append(errs, Error{
			File:     file,
			Pointer:  "/manifest/" + field,
			Rule:     "entry-agreement",
			Message:  fmt.Sprintf("the inline manifest's %s is %q but the entry's is %q; two copies of one fact drift", field, inlineValue, pointerValue),
			Expected: pointerValue,
		})
	}
	check("id", e.ID, e.Manifest.ID)
	check("repo", e.Repo, e.Manifest.Repo)
	check("ref", e.Ref, e.Manifest.Ref)
	return errs
}

// prefixPointers rewrites pointers produced against a standalone manifest so
// they name where that manifest sits inside the entry.
func prefixPointers(prefix string, errs []Error) []Error {
	for i := range errs {
		switch errs[i].Pointer {
		case "", "/":
			errs[i].Pointer = prefix
		default:
			errs[i].Pointer = prefix + errs[i].Pointer
		}
	}
	return errs
}

// ValidateAny validates whichever document data is, and says which it took it
// for. This is what the editor, import and `helm validate` call when the text
// could be either.
func ValidateAny(name string, data []byte) (Kind, Result, error) {
	switch k := DetectKind(data); k {
	case KindPointer:
		res, err := ValidateEntryBytes(name, data)
		return KindPointer, res, err
	case KindUnknown:
		return KindUnknown, Result{File: name, Errors: []Error{{
			File: name, Pointer: "/", Rule: "parse",
			Message: "this is not a helmstudio manifest or a registry entry; it does not parse as a YAML mapping",
		}}}, nil
	default:
		res, err := ValidateBytes(name, data)
		return KindManifest, res, err
	}
}

// LoadEntryBytes validates a registry entry and returns it decoded.
func LoadEntryBytes(name string, data []byte) (*Entry, Result, error) {
	res, err := ValidateEntryBytes(name, data)
	if err != nil || !res.OK() {
		return nil, res, err
	}
	e := new(Entry)
	if err := yaml.Unmarshal(data, e); err != nil {
		return nil, Result{}, fmt.Errorf("registry entry %s passed validation but failed typed decode: %w", name, err)
	}
	if e.Digest, err = digest(data); err != nil {
		return nil, Result{}, fmt.Errorf("registry entry %s: computing its digest: %w", name, err)
	}
	if e.Manifest != nil {
		// The pointer is the authority for the three shared fields, so an
		// inline manifest that leaves them out is completed from it rather
		// than being carried around half-filled.
		if e.Manifest.ID == "" {
			e.Manifest.ID = e.ID
		}
		if e.Manifest.Repo == "" {
			e.Manifest.Repo = e.Repo
		}
		if e.Manifest.Ref == "" {
			e.Manifest.Ref = e.Ref
		}
		if e.Manifest.Digest, err = digest(data); err != nil {
			return nil, Result{}, fmt.Errorf("registry entry %s: computing its digest: %w", name, err)
		}
	}
	return e, res, nil
}

// LoadBytes validates a manifest given as bytes and returns it decoded.
func LoadBytes(name string, data []byte) (*Manifest, Result, error) {
	res, err := ValidateBytes(name, data)
	if err != nil || !res.OK() {
		return nil, res, err
	}
	m := new(Manifest)
	if err := yaml.Unmarshal(data, m); err != nil {
		return nil, Result{}, fmt.Errorf("manifest %s passed validation but failed typed decode: %w", name, err)
	}
	if m.Digest, err = digest(data); err != nil {
		return nil, Result{}, fmt.Errorf("manifest %s: computing its digest: %w", name, err)
	}
	return m, res, nil
}
