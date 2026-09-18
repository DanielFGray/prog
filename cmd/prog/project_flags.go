package main

import (
	"fmt"
	"strings"
)

// rejectDuplicateProjectFlags errors when -p/--project appears more than once.
// Agents often invent a second -p for --priority or --parent; pflag would
// silently overwrite the project with that value.
func rejectDuplicateProjectFlags(args []string) error {
	if countProjectFlags(args) > 1 {
		return fmt.Errorf("-p/--project may only be specified once (did you mean --priority or --parent?)")
	}
	return nil
}

// countProjectFlags counts -p / --project occurrences in argv (stops at --).
func countProjectFlags(args []string) int {
	count := 0
	for _, a := range args {
		if a == "--" {
			break
		}
		switch {
		case a == "-p", a == "--project":
			count++
		case strings.HasPrefix(a, "-p="), strings.HasPrefix(a, "--project="):
			count++
		case len(a) > 2 && a[0] == '-' && a[1] != '-' && a[1] == 'p':
			// -pVALUE attached form. Also matches mistaken -priority / -parent,
			// which pflag itself parses as -p with a glued value.
			count++
		}
	}
	return count
}

// validateProjectName rejects names that are almost certainly a mistaken
// --priority or --parent passed as -p/--project (see ts-320c33).
func validateProjectName(name string) error {
	switch name {
	case "1", "2", "3":
		return fmt.Errorf("project %q looks like a priority value; use --priority %s (-p is --project)", name, name)
	}
	if looksLikeItemID(name) {
		return fmt.Errorf("project %q looks like an item ID; use --parent %s (-p is --project)", name, name)
	}
	return nil
}

// looksLikeItemID reports whether name matches prog's ts-/ep- + 6 hex id shape.
func looksLikeItemID(name string) bool {
	if len(name) != 9 {
		return false
	}
	prefix := name[:3]
	if prefix != "ts-" && prefix != "ep-" {
		return false
	}
	for i := 3; i < 9; i++ {
		c := name[i]
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}
