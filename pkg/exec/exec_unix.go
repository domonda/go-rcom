//go:build unix

package exec

import "syscall"

// The command is the leader of its own process group (Setsid in setSysProcAttr),
// so its group can be killed with the negative process ID.
// See https://medium.com/@felixge/killing-a-child-process-and-all-of-its-children-in-go-54079af94773

func (c *Cmd) kill() error {
	if c.killSubProc {
		return killProcessGroup(c.cmd.Process.Pid)
	}
	return c.cmd.Process.Kill()
}

// killSubProcesses kills the processes left in the process group
// of the command after it exited
func (c *Cmd) killSubProcesses() {
	if c.killSubProc {
		_ = killProcessGroup(c.cmd.Process.Pid)
	}
}

func killProcessGroup(pid int) error {
	return syscall.Kill(-pid, syscall.SIGKILL) // minus sign is on purpose
}
