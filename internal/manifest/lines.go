package manifest

import (
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// attachLines fills in Line on every error whose Pointer resolves against
// root, the manifest's own parsed yaml.Node tree. PRD R6 asks for line
// numbers in validate's output; a JSON pointer alone satisfies "which field"
// but not "where in the file to look."
func attachLines(root *yaml.Node, errs []Error) {
	for i := range errs {
		errs[i].Line = lineForPointer(root, errs[i].Pointer)
	}
}

// lineForPointer walks a YAML document node following the segments of a
// JSON pointer (RFC 6901) and returns the 1-based line of whatever it finds.
// A pointer segment that names a property absent from the document (the
// common case for a "missing properties" error) returns the line of the
// containing mapping instead — the field doesn't exist to point at, but
// where it belongs does.
func lineForPointer(root *yaml.Node, pointer string) int {
	if root == nil {
		return 0
	}
	node := root
	if node.Kind == yaml.DocumentNode && len(node.Content) > 0 {
		node = node.Content[0]
	}
	if node == nil {
		return 0
	}
	if pointer == "" || pointer == "/" {
		return node.Line
	}

	for _, raw := range strings.Split(strings.TrimPrefix(pointer, "/"), "/") {
		if raw == "" {
			continue
		}
		seg := unescapePointerToken(raw)
		switch node.Kind {
		case yaml.MappingNode:
			found := false
			for i := 0; i+1 < len(node.Content); i += 2 {
				if node.Content[i].Value == seg {
					node = node.Content[i+1]
					found = true
					break
				}
			}
			if !found {
				return node.Line // property doesn't exist; point at the mapping it would join
			}
		case yaml.SequenceNode:
			idx, err := strconv.Atoi(seg)
			if err != nil || idx < 0 || idx >= len(node.Content) {
				return node.Line
			}
			node = node.Content[idx]
		default:
			return node.Line
		}
	}
	return node.Line
}

func unescapePointerToken(s string) string {
	s = strings.ReplaceAll(s, "~1", "/")
	s = strings.ReplaceAll(s, "~0", "~")
	return s
}
