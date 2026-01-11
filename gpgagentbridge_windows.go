package main

import (
	"context"
	"log"
	"os/exec"
	"syscall"
	"time"
)

func launchGpgAgent(gpgconfCmd []string) error {
	cmdParts := make([]string, len(gpgconfCmd), len(gpgconfCmd)+2)
	copy(cmdParts, gpgconfCmd)
	cmdParts = append(cmdParts, "--launch", "gpg-agent")
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(5*time.Second))
	defer cancel()
	cmd := exec.CommandContext(ctx, cmdParts[0], cmdParts[1:]...)
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP}
	out, err := cmd.CombinedOutput()
	if len(out) > 0 {
		log.Printf("command output: %s", string(out))
	}
	return err
}
