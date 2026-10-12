package versions

import "testing"

func TestPinnedVersionsAreDefined(t *testing.T) {
	for name, val := range map[string]string{
		"Context7MCP": Context7MCP,
	} {
		if val == "" {
			t.Errorf("%s must not be empty", name)
		}
	}
}
