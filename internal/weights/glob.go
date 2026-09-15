package weights

import (
	"path"
	"strings"
)

// Match reports whether a repo-relative, slash-separated file path matches a
// weights[].files pattern (docs/decisions.md, "M3 install and weights"). A
// pattern is matched one path segment at a time: `*`, `?` and `[...]` never
// cross a `/`, and a segment that is exactly `**` matches zero or more whole
// segments. So `*.safetensors` matches only at the repo root, `FL2VA/**`
// matches everything under FL2VA, and `config.json` matches that one file.
func Match(pattern, file string) bool {
	return matchSegments(strings.Split(pattern, "/"), strings.Split(file, "/"))
}

func matchSegments(pat, name []string) bool {
	for len(pat) > 0 {
		if pat[0] == "**" {
			rest := pat[1:]
			for i := 0; i <= len(name); i++ {
				if matchSegments(rest, name[i:]) {
					return true
				}
			}
			return false
		}
		if len(name) == 0 {
			return false
		}
		if ok, err := path.Match(pat[0], name[0]); err != nil || !ok {
			return false
		}
		pat, name = pat[1:], name[1:]
	}
	return len(name) == 0
}

// matchesAny reports whether file matches any pattern. No patterns means the
// whole repository.
func matchesAny(patterns []string, file string) bool {
	if len(patterns) == 0 {
		return true
	}
	for _, p := range patterns {
		if Match(p, file) {
			return true
		}
	}
	return false
}
