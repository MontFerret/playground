package httpapi

import "strings"

func allowedRequestHeaders(values []string) bool {
	for _, line := range values {
		for name := range strings.SplitSeq(line, ",") {
			if !strings.EqualFold(strings.TrimSpace(name), "Content-Type") {
				return false
			}
		}
	}

	return true
}
