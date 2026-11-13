package tools

import (
	"testing"
)

// Started by the user the app never acts as a tool, whatever the arguments.
func TestNoCommandLineTool(t *testing.T) {
	t.Setenv(toolEnv, "")
	RunEmbeddedTool() // must return (not exit) without the internal variable
}
