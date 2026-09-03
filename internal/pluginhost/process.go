package pluginhost

import (
	"io"
	"os"
	"os/exec"
)

// CommandSpec is the argv-separated description of one plugin process launch.
// It never contains a shell command string; Path and Args are passed to
// os/exec verbatim.
type CommandSpec struct {
	Path string
	Args []string
	Env  []string
}

// Process is the running plugin process as seen by the Supervisor. It is the
// seam that lets tests inject an in-memory fake instead of a real binary.
type Process interface {
	Wait() error
	Signal(os.Signal) error
	Kill() error
	Pid() int
	Stdout() io.ReadCloser
	Stderr() io.ReadCloser
}

// Runner starts plugin processes from a CommandSpec.
type Runner interface {
	Start(spec CommandSpec) (Process, error)
}

// ExecRunner is the production Runner backed by os/exec.
type ExecRunner struct{}

// Start launches the process and attaches platform process-tree control
// (Job Object on Windows, process group elsewhere).
func (ExecRunner) Start(spec CommandSpec) (Process, error) {
	cmd := exec.Command(spec.Path, spec.Args...)
	cmd.Env = spec.Env
	_ = prepareProcessControl(cmd)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}

	killer, err := newProcessKiller(cmd)
	if err != nil {
		killer = &directKiller{proc: cmd.Process}
	}
	return &execProcess{cmd: cmd, killer: killer, stdout: stdout, stderr: stderr}, nil
}

// processKiller terminates a process and, when the platform supports it, its
// descendants.
type processKiller interface {
	Kill() error
}

// directKiller only kills the direct child and is the fallback when the
// platform tree control cannot be attached.
type directKiller struct {
	proc *os.Process
}

func (k *directKiller) Kill() error {
	if k == nil || k.proc == nil {
		return nil
	}
	return k.proc.Kill()
}

type execProcess struct {
	cmd    *exec.Cmd
	killer processKiller
	stdout io.ReadCloser
	stderr io.ReadCloser
}

func (p *execProcess) Wait() error                { return p.cmd.Wait() }
func (p *execProcess) Pid() int                   { return p.cmd.Process.Pid }
func (p *execProcess) Stdout() io.ReadCloser      { return p.stdout }
func (p *execProcess) Stderr() io.ReadCloser      { return p.stderr }
func (p *execProcess) Signal(sig os.Signal) error { return p.cmd.Process.Signal(sig) }
func (p *execProcess) Kill() error {
	if p.killer != nil {
		if err := p.killer.Kill(); err == nil {
			return nil
		}
	}
	return p.cmd.Process.Kill()
}
