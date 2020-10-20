package exec

func (c *Cmd) setSysProcAttr() {
	// Nop for Windows
}

func (c *Cmd) kill() error {
	return c.cmd.Process.Kill()
}
