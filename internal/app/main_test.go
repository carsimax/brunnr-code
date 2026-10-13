package app

import (
	"os"
	"testing"

	"github.com/carsimax/brunnr-code/internal/cli"
	"github.com/carsimax/brunnr-code/internal/components/communitytool"
	"github.com/carsimax/brunnr-code/internal/model"
)

// TestMain keeps every full install in this package from running the real
// CodeGraph installer, which would call npm or a codegraph CLI found on PATH
// with the developer's real HOME.
func TestMain(m *testing.M) {
	restore := cli.SetCodeGraphInstallerForTest(func(id model.CommunityToolID, _, _ string, _ communitytool.Runner, _ communitytool.Detector) (communitytool.Result, error) {
		return communitytool.Result{Tool: id}, nil
	})
	code := m.Run()
	restore()
	os.Exit(code)
}
