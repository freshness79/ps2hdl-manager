package disc

import (
	"regexp"
)

var FileIDRe = regexp.MustCompile(`(?i)([A-Z]{4})[_\-]?(\d{3})\.?(\d{2})`)
