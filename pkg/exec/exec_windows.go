package exec

func (c *Cmd) setSysProcAttr() {
	// Nop for Windows
}

func (c *Cmd) kill() error {
	return c.cmd.Process.Kill()
}

// killSubProcesses is not supported on Windows
// because processes have no process group to kill
func (c *Cmd) killSubProcesses() {}
