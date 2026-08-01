package ghtmx

import (
	_ "embed"
	"strings"
)

//go:embed .version
var version string

func Version() string {
	return "v" + strings.TrimSpace(version)
}
