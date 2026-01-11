package main

import (
	// "log"
	"os/exec"
	"syscall"
)

func launchGpgAgent(gpgconfCmd []string) error {
	const DETACHED_PROCESS = 0x00000008
	cmdParts := make([]string, len(gpgconfCmd), len(gpgconfCmd)+2)
	copy(cmdParts, gpgconfCmd)
	cmdParts = append(cmdParts, "--launch", "gpg-agent")
	cmd := exec.Command(cmdParts[0], cmdParts[1:]...)
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP | DETACHED_PROCESS}
	_, err := cmd.CombinedOutput()
	// log.Printf("start output: %s", string(out))
	return err
}
