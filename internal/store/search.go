package store

import "strings"

func containsFold(s, q string) bool { return strings.Contains(strings.ToLower(s), strings.ToLower(q)) }
