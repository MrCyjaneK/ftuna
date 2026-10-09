// Package aibadge appends the Chum AI-score image used in catalog descriptions.
package aibadge

import "strings"

const base = "https://raw.githubusercontent.com/sailfishos-chum/sailfishos-chum-gui/main/icons/theme/ai-score/svgs/icons/"

// Append puts the badge for code at the end of description.
// An empty or unrecognized code uses the unknown badge.
func Append(description, code string) string {
	tag := `<img src="` + base + file(code) + `">`
	description = strings.TrimRight(description, "\n")
	if description == "" {
		return tag
	}
	return description + "\n\n" + tag
}

func file(code string) string {
	switch strings.ToUpper(strings.TrimSpace(code)) {
	case "H", "A", "B", "C", "V":
		return "AI-score-" + strings.ToUpper(strings.TrimSpace(code)) + ".svg"
	case "X":
		return "AI-score-will-not-tell.svg"
	default:
		return "AI-score-unknown.svg"
	}
}
