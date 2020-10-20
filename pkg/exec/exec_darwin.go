package exec

import "syscall"

func (c *Cmd) setSysProcAttr() {
	if c.killSubProc {
		c.cmd.SysProcAttr = &syscall.SysProcAttr{
			Setsid: true,
		}
	}
}

func (c *Cmd) kill() error {
	// See https://medium.com/@felixge/killing-a-child-process-and-all-of-its-children-in-go-54079af94773
	if c.killSubProc {
		return syscall.Kill(-c.cmd.Process.Pid, syscall.SIGKILL) // minus sign is on purpose
	}
	return c.cmd.Process.Kill()
}
