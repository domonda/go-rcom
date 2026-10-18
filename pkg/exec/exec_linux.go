package exec

import "syscall"

func (c *Cmd) setSysProcAttr() {
	if c.killSubProc {
		c.cmd.SysProcAttr = &syscall.SysProcAttr{
			Setsid:    true,
			Pdeathsig: syscall.SIGKILL,
		}
	}
}
