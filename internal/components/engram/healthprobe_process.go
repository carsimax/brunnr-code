package engram

import (
	"errors"
	"os/exec"
	"syscall"
)

// startProbeProcessTree isolates the probe in its own process group so a
// child retaining stdout cannot outlive the bounded MCP handshake.
func startProbeProcessTree(command *exec.Cmd) (func() error, error) {
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := command.Start(); err != nil {
		return nil, err
	}
	return func() error {
		// The leader is never reaped before this kill, so its process group
		// cannot be reused. A group of only exited members reports ESRCH or,
		// on macOS, EPERM. Both mean there is nothing left to stop.
		if err := syscall.Kill(-command.Process.Pid, syscall.SIGKILL); err != nil && !errors.Is(err, syscall.ESRCH) && !errors.Is(err, syscall.EPERM) {
			return err
		}
		return nil
	}, nil
}
