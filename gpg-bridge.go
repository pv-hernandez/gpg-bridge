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
	"strings"
	"syscall"
	"time"
)

func getSocketFile(socketName string, gpgconfCmd []string) (string, error) {
	// log.Printf("getting socket file for %s", socketName)
	cmdParts := make([]string, len(gpgconfCmd), len(gpgconfCmd)+2)
	copy(cmdParts, gpgconfCmd)
	cmdParts = append(cmdParts, "--list-dirs", socketName)
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
	path := strings.TrimRight(string(out), "\n\r")
	log.Printf("got socket file '%s'", path)
	socketFile := path
	return socketFile, nil
}

func readPortAndNonce(socketFile string) (int, []byte, bool, error) {
	n := 0
	buffer := [54]byte{}
	{
		file, err := os.Open(socketFile)
		if err != nil {
			return 0, nil, false, fmt.Errorf("Failed to open socket-file '%s': %w", socketFile, err)
		}
		defer file.Close()

		reader := bufio.NewReader(file)

		n, err = reader.Read(buffer[:])
		if err != nil {
			return 0, nil, false, fmt.Errorf("Failed to read socket-file '%s': %w", socketFile, err)
		}
	}
	if n == 0 {
		return 0, nil, false, fmt.Errorf("Socket-file '%s' is empty", socketFile)
	}
	port := 0
	nonce := [16]byte{}
	cygwin := false
	offset := 0
	var err error
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
			return 0, nil, false, fmt.Errorf("Failed parsing port '%s': %w", portStr, err)
		}

		offset = portEnd + 1
		socketType := string(buffer[offset : offset+1])
		if socketType != "s" {
			return 0, nil, false, fmt.Errorf("Unsupported socket type %s", socketType)
		}
		offset += 2
		for i := range 4 {
			// convert from little endian ascii to big endian binary
			part := buffer[offset : offset+8]
			bin := [4]byte{}
			_, err = hex.Decode(bin[:], part)
			if err != nil {
				return 0, nil, false, fmt.Errorf("Failed decoding nonce part %d: %w", i, err)
			}
			num := binary.LittleEndian.Uint32(bin[:])
			binary.BigEndian.PutUint32(nonce[4*i:], num)
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
			return 0, nil, false, fmt.Errorf("Failed parsing port '%s': %w", portStr, err)
		}
		offset = portStrEnd + 1
		copy(nonce[:], buffer[offset:])
	}
	return port, nonce[:], cygwin, nil
}

func socketConnect(socketFile string) (net.Conn, error) {
	info, err := os.Stat(socketFile)
	if err != nil {
		return nil, fmt.Errorf("Failed to stat socket file '%s': %v", socketFile, err)
	}

	var conn net.Conn

	mode := info.Mode()
	if mode.IsRegular() {
		// emulated unix domain socket
		port, nonce, cygwin, err := readPortAndNonce(socketFile)
		if err != nil {
			return nil, fmt.Errorf("Failed to read port and nonce from socket file '%s': %w", socketFile, err)
		}

		address := fmt.Sprintf("127.0.0.1:%d", port)
		conn, err = net.DialTimeout("tcp4", address, time.Duration(1*time.Second))
		if err != nil {
			return nil, fmt.Errorf("Failed to connect to socket '%s': %w", address, err)
		}
		defer func() {
			if err != nil {
				conn.Close()
			}
		}()

		_, err = conn.Write(nonce[:])
		if err != nil {
			return nil, fmt.Errorf("Failed to send nonce %v to socket: %w", nonce, err)
		}
		if cygwin {
			_, err := conn.Read(nonce[:])
			if err != nil {
				return nil, fmt.Errorf("Failed to receive nonce echo from socket: %w", err)
			}

			pid := os.Getpid()
			binary.BigEndian.PutUint32(nonce[:4], uint32(pid))
			binary.BigEndian.PutUint32(nonce[4:8], 0)
			binary.BigEndian.PutUint32(nonce[8:12], 0)
			_, err = conn.Write(nonce[:12])
			if err != nil {
				return nil, fmt.Errorf("Failed to send credentials %v to socket: %w", nonce[:12], err)
			}
			_, err = conn.Read(nonce[:12])
			if err != nil {
				return nil, fmt.Errorf("Failed to receive credentials from socket: %w", err)
			}
		}
	} else if mode&os.ModeSocket != 0 {
		// unix domain socket
		conn, err = net.Dial("unix", socketFile)
		if err != nil {
			return nil, fmt.Errorf("Failed to connect to socket '%s': %w", socketFile, err)
		}
	}
	return conn, nil
}

func main() {
	log.Default().SetOutput(os.Stderr)
	for _, envvar := range os.Environ() {
		log.Printf("env: %v", envvar)
	}
	if len(os.Args) < 2 {
		log.Panicf("Usage: %s <socket-name>", os.Args[0])
	}

	suffixes := [...]string{"CMD", "ARG1", "ARG2", "ARG3", "ARG4", "ARG5", "ARG6", "ARG7", "ARG8", "ARG9"}
	socketName := os.Args[1]
	gpgconfCmd := make([]string, 0, len(suffixes))
	for i := range len(suffixes) {
		value, exists := os.LookupEnv(fmt.Sprintf("GPG_BRIDGE_GPGCONF_%s", suffixes[i]))
		if !exists {
			if i == 0 {
				value = "gpgconf"
				gpgconfCmd = append(gpgconfCmd, value)
			}
			break
		}
		gpgconfCmd = append(gpgconfCmd, value)
	}

	socketFile, err := getSocketFile(socketName, gpgconfCmd)
	if err != nil {
		log.Panicf("Failed to get socket file from socket name '%s': %v", socketName, err)
	}

	conn, err := socketConnect(socketFile)
	if err != nil {
		log.Printf("failed to connect to socket on first try")
		if errors.Is(err, os.ErrNotExist) || errors.Is(err, syscall.ECONNREFUSED) || errors.Is(err, syscall.Errno(0x274d)) {
			log.Printf("trying to start gpg-agent")
			err = launchGpgAgent(gpgconfCmd)
			if err != nil {
				log.Panicf("Failed to start gpg-agent service: %v", err)
			}
			log.Printf("retrying to connect")
			conn, err = socketConnect(socketFile)
		}
		if err != nil {
			log.Panicf("Failed to connect to socket: %v", err)
		}
	}
	defer conn.Close()

	// log.Printf("Connected to socket-file '%s'", socketFile)

	done := make(chan struct{})

	go func() {
		if _, err := io.Copy(os.Stdout, conn); err != nil {
			if errors.Is(err, io.EOF) || errors.Is(err, net.ErrClosed) {
				return
			}
			log.Printf("Error reading from socket: %v", err)
		}
		done <- struct{}{}
	}()

	go func() {
		if _, err := io.Copy(conn, os.Stdin); err != nil {
			log.Printf("Error writing to socket: %v", err)
		}
		done <- struct{}{}
	}()

	<-done
	log.Printf("connection closed")
}
