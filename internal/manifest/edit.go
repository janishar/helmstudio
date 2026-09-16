package manifest

import (
	"fmt"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// Editing a manifest as a document rather than as a value
// (docs/decisions.md M7 Q17).
//
// The obvious implementation — decode to a struct, change a field, re-encode —
// throws away every comment and every choice of order and style the author
// made, and hands back a file they did not write. A person who opens the
// editor to change one port and gets their comments deleted will not open it
// twice.
//
// So an edit is applied to the YAML node tree: find the node the pointer
// names, replace its value, leave everything around it alone. This is also why
// the page sends a pointer and a value rather than sending back whole text —
// the daemon is the only thing that parses YAML here, and a JavaScript parser
// on the page would disagree with this one about anchors, merge keys, `on` and
// duplicate keys, and would show one manifest while validating another.

// Edit applies one change to YAML text and returns the new text.
//
// pointer is an RFC 6901 JSON pointer ("/processes/0/cmd"). A nil value
// removes the key or the array element. A pointer whose parent exists but whose
// last segment does not adds it; a pointer whose parent does not exist is an
// error, because guessing at the shape of what is missing is how an editor
// writes something nobody asked for.
func Edit(text []byte, pointer string, value any) ([]byte, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal(text, &doc); err != nil {
		return nil, fmt.Errorf("this is not YAML: %w", err)
	}
	if doc.Kind != yaml.DocumentNode || len(doc.Content) == 0 {
		// An empty document is a legitimate starting point for the editor:
		// the first field typed into a new manifest lands here.
		doc = yaml.Node{Kind: yaml.DocumentNode, Content: []*yaml.Node{{Kind: yaml.MappingNode, Tag: "!!map"}}}
	}

	segments, err := parsePointer(pointer)
	if err != nil {
		return nil, err
	}
	if len(segments) == 0 {
		return nil, fmt.Errorf("the pointer is empty; it must name a field, such as /name or /processes/0/cmd")
	}

	if err := applyEdit(doc.Content[0], segments, value); err != nil {
		return nil, err
	}

	var out strings.Builder
	enc := yaml.NewEncoder(&out)
	// Two spaces is what every manifest in this repository uses; re-encoding
	// with the library's four would rewrite every line of a file the author
	// only asked to change one field of.
	enc.SetIndent(2)
	if err := enc.Encode(&doc); err != nil {
		return nil, fmt.Errorf("re-encoding the document: %w", err)
	}
	if err := enc.Close(); err != nil {
		return nil, fmt.Errorf("re-encoding the document: %w", err)
	}
	return []byte(out.String()), nil
}

// parsePointer splits an RFC 6901 pointer, undoing its two escapes.
func parsePointer(p string) ([]string, error) {
	if p == "" || p == "/" {
		return nil, nil
	}
	if !strings.HasPrefix(p, "/") {
		return nil, fmt.Errorf("pointer %q does not start with /", p)
	}
	parts := strings.Split(strings.TrimPrefix(p, "/"), "/")
	for i, s := range parts {
		s = strings.ReplaceAll(s, "~1", "/")
		parts[i] = strings.ReplaceAll(s, "~0", "~")
	}
	return parts, nil
}

func applyEdit(node *yaml.Node, segments []string, value any) error {
	last := len(segments) == 1
	seg := segments[0]

	switch node.Kind {
	case yaml.MappingNode:
		for i := 0; i+1 < len(node.Content); i += 2 {
			if node.Content[i].Value != seg {
				continue
			}
			if !last {
				return applyEdit(node.Content[i+1], segments[1:], value)
			}
			if value == nil {
				// Removing takes the key with the value, and the comment that
				// sits on the key — which belonged to the thing being removed.
				node.Content = append(node.Content[:i], node.Content[i+2:]...)
				return nil
			}
			return setValue(node.Content[i+1], value)
		}
		if !last {
			return fmt.Errorf("no %q in this document; add it before editing what is inside it", seg)
		}
		if value == nil {
			return nil // removing what is not there is what was asked for
		}
		key := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: seg}
		val := &yaml.Node{}
		if err := setValue(val, value); err != nil {
			return err
		}
		node.Content = append(node.Content, key, val)
		return nil

	case yaml.SequenceNode:
		i, err := strconv.Atoi(seg)
		if err != nil {
			return fmt.Errorf("%q is a list, so the pointer needs an index, not %q", strings.Join(segments, "/"), seg)
		}
		if i == len(node.Content) && last && value != nil {
			val := &yaml.Node{}
			if err := setValue(val, value); err != nil {
				return err
			}
			node.Content = append(node.Content, val)
			return nil
		}
		if i < 0 || i >= len(node.Content) {
			return fmt.Errorf("index %d is outside a list of %d", i, len(node.Content))
		}
		if !last {
			return applyEdit(node.Content[i], segments[1:], value)
		}
		if value == nil {
			node.Content = append(node.Content[:i], node.Content[i+1:]...)
			return nil
		}
		return setValue(node.Content[i], value)

	case yaml.AliasNode:
		return fmt.Errorf("that field comes from a YAML anchor; editing it would change every place the anchor is used, so it has to be edited by hand")

	default:
		return fmt.Errorf("cannot look inside %q: it is a single value, not a mapping or a list", seg)
	}
}

// setValue replaces what a node holds while keeping its comments, because the
// comment above a field is about the field, not about the value it had today.
func setValue(node *yaml.Node, value any) error {
	head, line, foot := node.HeadComment, node.LineComment, node.FootComment
	fresh := &yaml.Node{}
	if err := fresh.Encode(value); err != nil {
		return fmt.Errorf("that value cannot be written as YAML: %w", err)
	}
	style := node.Style
	*node = *fresh
	// A flow-style value stays flow-style: the weights in these manifests are
	// written one per line as { name: …, repo: … }, and re-encoding them as
	// block mappings would reformat a file nobody asked to reformat.
	if style == yaml.FlowStyle && (node.Kind == yaml.MappingNode || node.Kind == yaml.SequenceNode) {
		node.Style = yaml.FlowStyle
	}
	node.HeadComment, node.LineComment, node.FootComment = head, line, foot
	return nil
}
