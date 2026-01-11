package main

import (
	"bufio"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"os/exec"
	"strconv"
	"syscall"
)

func getSocketFile(socketName string, gpgconfCmd []string, cygpathCmd []string) (string, error) {
	// log.Printf("getting socket file for %s", socketName)
	cmdParts := make([]string, len(gpgconfCmd), len(gpgconfCmd)+2)
	copy(cmdParts, gpgconfCmd)
	cmdParts = append(cmdParts, "--list-dirs", socketName)
	log.Printf("commands: %v", cmdParts)
	cmd := exec.Command(cmdParts[0], cmdParts[1:]...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		log.Printf("command error: %s", string(out))
		return "", err
		// if errors.Is(err, &exec.ExitError{}) {
		// 	// log.Printf("gpgconf --list-dirs '%s'\n%s", socketName, )
		// }
		// log.Panicf("Failed to get windows gpg socket dir '%s': %v", socketName, err)
	}
	if len(out) == 0 {
		return "", fmt.Errorf("Invalid socket name: '%s'", socketName)
	}
	path := string(out[:len(out)-1])
	if path[0] == '/' {
		// cygwin version of gnupg
		// log.Printf("converting cygwin path to windows '%s'", string(out))
		cmdParts = make([]string, len(cygpathCmd), len(cygpathCmd)+2)
		copy(cmdParts, cygpathCmd)
		cmdParts = append(cmdParts, "-w", path)
		cmd = exec.Command(cmdParts[0], cmdParts[1:]...)
		out, err = cmd.CombinedOutput()
		if err != nil {
			return "", err
			// log.Panicf("Failed convert cygwin path '%s' to windows path: %v", string(out), err)
		}
		if len(out) == 0 {
			return "", fmt.Errorf("Invalid socket path: '%s'", path)
		}
		path = string(out[:len(out)-1])
	}
	log.Printf("got socket file '%s'", path)
	socketFile := path
	return socketFile, nil
}

func main() {
	log.Default().SetOutput(os.Stderr)
	if len(os.Args) < 2 {
		log.Panicf("Usage: %s <socket-name>", os.Args[0])
	}

	suffixes := [...]string{"CMD", "ARG1", "ARG2", "ARG3", "ARG4"}
	socketName := os.Args[1]
	gpgconfCmd := make([]string, 0, len(suffixes))
	for i := range len(suffixes) {
		value, exists := os.LookupEnv(fmt.Sprintf("GPGAGENTBRIDGE_GPGCONF_%s", suffixes[i]))
		if !exists {
			if i == 0 {
				value = "gpgconf"
				gpgconfCmd = append(gpgconfCmd, value)
			}
			break
		}
		gpgconfCmd = append(gpgconfCmd, value)
	}
	cygpathCmd := make([]string, 0, len(suffixes))
	for i := range len(suffixes) {
		value, exists := os.LookupEnv(fmt.Sprintf("GPGAGENTBRIDGE_CYGPATH_%s", suffixes[i]))
		if !exists {
			if i == 0 {
				value = "cygpath"
				cygpathCmd = append(cygpathCmd, value)
			}
			break
		}
		cygpathCmd = append(cygpathCmd, value)
	}

	socketFile, err := getSocketFile(socketName, gpgconfCmd, cygpathCmd)
	if err != nil {
		log.Panicf("Failed to get socket file from socket name '%s': %v", socketName, err)
	}

	// log.Printf("trying stat")
	info, err := os.Stat(socketFile)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			// log.Printf("trying to start gpg-agent")
			err = launchGpgAgent(gpgconfCmd)
			if err != nil {
				log.Panicf("Failed to start gpg-agent service: %v", err)
			}
			// log.Printf("retrying stat")
			info, err = os.Stat(socketFile)
		}
		if err != nil {
			log.Panicf("Failed to stat socket-file '%s': %v", socketFile, err)
		}
	}

	var conn net.Conn

	mode := info.Mode()
	if mode.IsRegular() {
		// emulated unix domain socket
		file, err := os.Open(socketFile)
		if err != nil {
			log.Panicf("Failed to open socket-file '%s': %v", socketFile, err)
		}
		defer file.Close()

		reader := bufio.NewReader(file)

		buffer := [54]byte{}
		n, err := reader.Read(buffer[:])
		if err != nil {
			log.Panicf("Failed to read socket-file '%s': %v", socketFile, err)
		}
		if n == 0 {
			log.Panicf("Socket-file '%s' is empty", socketFile)
		}
		port := 0
		nonce := [16]byte{}
		cygwin := false
		offset := 0
		if string(buffer[:10]) == "!<socket >" {
			// cygwin compatible unix socket emulation
			cygwin = true
			offset = 10
			portEnd := offset
			for ; portEnd < n && buffer[portEnd] != ' '; portEnd++ {
			}
			portStr := string(buffer[offset:portEnd])
			port, err = strconv.Atoi(portStr)
			if err != nil {
				log.Panicf("Failed parsing port '%s': %v", portStr, err)
			}

			offset = portEnd + 1
			socketType := string(buffer[offset : offset+1])
			if socketType != "s" {
				log.Panicf("Unsupported socket type %s", socketType)
			}
			offset += 2
			for i := range 4 {
				// convert into little endian binary
				part := buffer[offset : offset+8]
				for j := range 4 {
					s := 6 - 2*j
					d := j + 4*i
					_, err = hex.Decode(nonce[d:d+1], part[s:s+2])
					if err != nil {
						log.Panicf("Failed decoding nounce part %d byte %d: %v", i, j, err)
					}
				}
				offset += 9
			}
		} else {
			// libassuan compatible unix socket emulation
			portStrEnd := 0
			for ; portStrEnd < n && buffer[portStrEnd] != '\n'; portStrEnd++ {
			}
			portStr := string(buffer[offset:portStrEnd])
			port, err = strconv.Atoi(portStr)
			if err != nil {
				log.Panicf("Failed parsing port '%s': %v", portStr, err)
			}
			offset = portStrEnd + 1
			copy(nonce[:], buffer[offset:])
		}

		address := fmt.Sprintf("127.0.0.1:%d", port)
		conn, err = net.Dial("tcp4", address)
		if err != nil {
			if errors.Is(err, syscall.ECONNREFUSED) || errors.Is(err, syscall.Errno(0x274d)) {
				// log.Printf("trying to start gpg-agent")
				err = launchGpgAgent(gpgconfCmd)
				if err != nil {
					log.Panicf("Failed to start gpg-agent service: %v", err)
				}
			}
			conn, err = net.Dial("tcp4", address)
			if err != nil {
				log.Panicf("Failed to connect to socket '%s': %v", address, err)
			}
		}
		defer conn.Close()

		_, err = conn.Write(nonce[:])
		if err != nil {
			log.Panicf("Failed to send nonce %v to socket: %v", nonce, err)
		}
		if cygwin {
			_, err := conn.Read(nonce[:])
			if err != nil {
				log.Panicf("Failed to receive nonce echo from socket: %v", err)
			}

			pid := os.Getpid()
			binary.BigEndian.PutUint32(nonce[:4], uint32(pid))
			binary.BigEndian.PutUint32(nonce[4:8], 0)
			binary.BigEndian.PutUint32(nonce[8:12], 0)
			_, err = conn.Write(nonce[:12])
			if err != nil {
				log.Panicf("Failed to send credentials %v to socket: %v", nonce[:12], err)
			}
			_, err = conn.Read(nonce[:12])
			if err != nil {
				log.Panicf("Failed to receive credentials from socket: %v", err)
			}
		}
	} else if mode&os.ModeSocket != 0 {
		// unix domain socket
		conn, err = net.Dial("unix", socketFile)
		if err != nil {
			log.Panicf("Failed to connect to socket '%s': %v", socketFile, err)
		}
		defer conn.Close()
	}

	// log.Printf("Connected to socket-file '%s'", socketFile)

	done := make(chan struct{})

	go func() {
		if _, err := io.Copy(os.Stdout, conn); err != nil {
			if errors.Is(err, io.EOF) || errors.Is(err, net.ErrClosed) {
				return
			}
			// log.Printf("Error reading from socket: %v", err)
		}
		done <- struct{}{}
	}()

	go func() {
		if _, err := io.Copy(conn, os.Stdin); err != nil {
			// log.Printf("Error writing to socket: %v", err)
		}
		done <- struct{}{}
	}()

	<-done
}
