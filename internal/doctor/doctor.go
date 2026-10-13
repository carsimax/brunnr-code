// Package doctor defines presentation-independent health check results and execution.
package doctor

import "context"

// CheckID is a stable identifier for a health check.
type CheckID string

const (
	CheckStateJSON             CheckID = "state:json"
	CheckInstalledAssetVersion CheckID = "installed:asset_version"
	CheckEngramReachable       CheckID = "engram:reachable"
	CheckDiskSpace             CheckID = "disk:space"
)

// ToolCheckID returns the stable check identifier for a tool binary.
func ToolCheckID(tool string) CheckID { return CheckID("tool:" + tool) }

// Status is the outcome of a health check.
type Status string

const (
	StatusPass Status = "pass"
	StatusWarn Status = "warn"
	StatusFail Status = "fail"
)

// RemedyID identifies a remedy that a future executor may implement.
type RemedyID string

const (
	RemedyInstallTool        RemedyID = "install-tool"
	RemedyRemoveDuplicates   RemedyID = "remove-duplicate-tools"
	RemedyInstall            RemedyID = "install"
	RemedyRepairState        RemedyID = "repair-state"
	RemedyInspectStateAccess RemedyID = "inspect-state-access"
	RemedySync               RemedyID = "sync"
	RemedyEditShellPath      RemedyID = "edit-shell-path"
	RemedyStartEngram        RemedyID = "start-engram"
	RemedyInspectEngram      RemedyID = "inspect-engram"
	RemedyFreeDiskSpace      RemedyID = "free-disk-space"
)

// Remedy describes a possible response without performing it.
type Remedy struct {
	ID          RemedyID
	Description string
}

// NewRemedy builds a remedy; it is guidance only and never executable.
func NewRemedy(id RemedyID, description string) *Remedy {
	return &Remedy{ID: id, Description: description}
}

// Result contains evidence produced by one check.
type Result struct {
	Name   CheckID
	Status Status
	Detail string
	Remedy *Remedy
}

// Report preserves checks in execution order.
type Report struct {
	Checks []Result
}

// Check is a read-only diagnostic operation.
type Check struct {
	ID  CheckID
	Run func(context.Context) Result
}

// Runner executes checks deterministically in declaration order.
type Runner struct {
	Checks []Check
}

// Run executes every check in declaration order and isolates panics per check.
func (r Runner) Run(ctx context.Context) Report {
	report := Report{Checks: make([]Result, 0, len(r.Checks))}
	for _, check := range r.Checks {
		result := runCheck(ctx, check)
		result.Name = check.ID
		report.Checks = append(report.Checks, result)
	}
	return report
}

func runCheck(ctx context.Context, check Check) (result Result) {
	defer func() {
		if recover() != nil {
			result = Result{Status: StatusFail, Detail: "check failed unexpectedly"}
		}
	}()
	return check.Run(ctx)
}
