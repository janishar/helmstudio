package common

import "strings"

// OwnerPrefix is the prefix every object of a user lives under.
func OwnerPrefix(owner string) string { return "u/" + owner + "/" }

// Owns reports whether key belongs to owner (and does not escape the prefix).
func Owns(owner, key string) bool {
	return owner != "" && strings.HasPrefix(key, OwnerPrefix(owner)) && !strings.Contains(key, "..") && !strings.Contains(key, "//")
}
