//go:build windows

package pluginhost

import (
	"os/exec"
	"unsafe"

	"golang.org/x/sys/windows"
)

// prepareProcessControl is a no-op on Windows: the Job Object is created and
// assigned after the child process exists (see newProcessKiller).
func prepareProcessControl(*exec.Cmd) error { return nil }

// jobKiller owns a Job Object that the plugin process is assigned to.
// Terminating (or closing) the job kills the plugin and any children it
// spawned, which gives the Supervisor orphan cleanup on Windows.
type jobKiller struct {
	job windows.Handle
}

func newProcessKiller(cmd *exec.Cmd) (processKiller, error) {
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return nil, err
	}
	info := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
	info.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	if _, err := windows.SetInformationJobObject(
		job,
		windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&info)),
		uint32(unsafe.Sizeof(info)),
	); err != nil {
		_ = windows.CloseHandle(job)
		return nil, err
	}

	proc, err := windows.OpenProcess(
		windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE,
		false,
		uint32(cmd.Process.Pid),
	)
	if err != nil {
		_ = windows.CloseHandle(job)
		return nil, err
	}
	defer windows.CloseHandle(proc)

	if err := windows.AssignProcessToJobObject(job, proc); err != nil {
		_ = windows.CloseHandle(job)
		return nil, err
	}
	return &jobKiller{job: job}, nil
}

func (k *jobKiller) Kill() error {
	// TerminateJobObject kills every process in the job. KILL_ON_JOB_CLOSE
	// additionally guarantees cleanup if the host process dies without Kill.
	err := windows.TerminateJobObject(k.job, 1)
	_ = windows.CloseHandle(k.job)
	return err
}
