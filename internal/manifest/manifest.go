package manifest

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"

	"gopkg.in/yaml.v3"
)

// decodeGeneric parses YAML into a structure the JSON Schema library can
// validate. yaml.v3 decodes integers as int and floats as float64; the
// schema library expects encoding/json's conventions (all numbers as
// float64), so the round trip through json.Marshal/Unmarshal normalises
// that rather than relying on the two decoders agreeing by accident.
func decodeGeneric(data []byte) (any, error) {
	var raw any
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("parsing yaml: %w", err)
	}
	jsonBytes, err := json.Marshal(raw)
	if err != nil {
		return nil, fmt.Errorf("converting to json: %w", err)
	}
	var doc any
	if err := json.Unmarshal(jsonBytes, &doc); err != nil {
		return nil, fmt.Errorf("re-parsing json: %w", err)
	}
	return doc, nil
}

// rejectMultiDocument errors on a file containing more than one YAML
// document ("---" separated). yaml.Unmarshal silently decodes only the
// first and drops the rest; a manifest is one document, and a second one a
// user meant to be read deserves an error, not silent loss.
func rejectMultiDocument(data []byte) error {
	dec := yaml.NewDecoder(bytes.NewReader(data))
	var first any
	if err := dec.Decode(&first); err != nil {
		if err == io.EOF {
			return nil // empty file; schema validation below will reject it
		}
		return err
	}
	var second any
	if err := dec.Decode(&second); err == nil {
		return fmt.Errorf("file contains more than one YAML document; a manifest is one document")
	} else if err != io.EOF {
		return err
	}
	return nil
}

// Validate reads and validates one manifest file: schema/manifest.json
// first, then the seven rules the schema cannot express. The semantic rules
// only run once the document is schema-valid — they assume a shape (process
// names present, weights named) that an invalid document is not guaranteed
// to have.
func Validate(file string) (Result, error) {
	data, err := os.ReadFile(file)
	if err != nil {
		return Result{}, fmt.Errorf("reading %s: %w", file, err)
	}

	if err := rejectMultiDocument(data); err != nil {
		return Result{File: file, Errors: []Error{{
			File: file, Pointer: "/", Rule: "parse", Message: err.Error(),
		}}}, nil
	}

	var root yaml.Node
	_ = yaml.Unmarshal(data, &root) // best-effort; a parse failure below is what's reported

	doc, err := decodeGeneric(data)
	if err != nil {
		return Result{File: file, Errors: []Error{{
			File: file, Pointer: "/", Rule: "parse", Message: err.Error(),
		}}}, nil
	}

	if errs := validateSchema(file, doc); len(errs) > 0 {
		attachLines(&root, errs)
		return Result{File: file, Errors: errs}, nil
	}

	var m Manifest
	if err := yaml.Unmarshal(data, &m); err != nil {
		// The document passed schema validation but the typed decode still
		// failed: a bug in this package's types, not a bad manifest.
		return Result{}, fmt.Errorf("manifest %s passed schema validation but failed typed decode: %w", file, err)
	}

	errs := validateSemantic(file, &m)
	attachLines(&root, errs)
	if errs == nil {
		errs = []Error{} // so a clean manifest serialises "errors": [] rather than null
	}
	return Result{File: file, Errors: errs}, nil
}
