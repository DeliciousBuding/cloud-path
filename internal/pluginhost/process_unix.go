//go:build !windows

package pluginhost

import (
	"os/exec"
	"syscall"
)

// prepareProcessControl puts the child in its own process group so the
// Supervisor can kill the plugin together with any children it spawns.
func prepareProcessControl(cmd *exec.Cmd) error {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	return nil
}

// processGroupKiller signals the whole process group.
type processGroupKiller struct {
	pid int
}

func newProcessKiller(cmd *exec.Cmd) (processKiller, error) {
	return &processGroupKiller{pid: cmd.Process.Pid}, nil
}

func (k *processGroupKiller) Kill() error {
	return syscall.Kill(-k.pid, syscall.SIGKILL)
}
