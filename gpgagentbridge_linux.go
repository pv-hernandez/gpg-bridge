package main

import (
	// "log"
	"os/exec"
	"syscall"
)

func launchGpgAgent(gpgconfCmd []string) error {
	cmdParts := make([]string, len(gpgconfCmd), len(gpgconfCmd)+2)
	copy(cmdParts, gpgconfCmd)
	cmdParts = append(cmdParts, "--launch", "gpg-agent")
	cmd := exec.Command(cmdParts[0], cmdParts[1:]...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	_, err := cmd.CombinedOutput()
	// log.Printf("start output: %s", string(out))
	return err
}
