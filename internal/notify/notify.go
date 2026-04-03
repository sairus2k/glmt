package notify

import (
	"fmt"
	"os"
	"strings"
)

// Send writes a terminal notification to stderr.
// Method must be "bel", "osc9", or "off". Unknown values are treated as "off".
func Send(method, message string) {
	switch method {
	case "bel":
		fmt.Fprint(os.Stderr, "\a")
	case "osc9":
		fmt.Fprintf(os.Stderr, "\033]9;%s\033\\", message)
	}
}

// FormatMessage builds a notification string from train results.
func FormatMessage(merged, skipped int, pipelineStatus string) string {
	var parts []string
	if merged > 0 {
		parts = append(parts, fmt.Sprintf("%d merged", merged))
	}
	if skipped > 0 {
		parts = append(parts, fmt.Sprintf("%d skipped", skipped))
	}
	msg := "glmt: " + strings.Join(parts, ", ")
	if pipelineStatus != "" {
		msg += " | pipeline " + pipelineStatus
	}
	return msg
}
