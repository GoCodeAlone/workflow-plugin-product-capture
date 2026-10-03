package provider

import "os/exec"

type managedBrowserProfile struct {
	Dir         string
	Generation  string
	ScopeSHA256 string
	verify      func() error
	configure   func(*exec.Cmd) error
	release     func() error
}

func (p managedBrowserProfile) Verify() error {
	if p.verify == nil {
		return nil
	}
	return p.verify()
}

func (p managedBrowserProfile) ConfigureCommand(cmd *exec.Cmd) error {
	if p.configure == nil {
		return nil
	}
	return p.configure(cmd)
}

func (p managedBrowserProfile) Release() error {
	if p.release == nil {
		return nil
	}
	return p.release()
}
