package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"strings"
	"sync/atomic"
	"time"
)

type (
	probeRequest struct {
		Network string
		Address string
		Payload string
	}

	probeResult struct {
		Connected bool
		Reply     string
		Denied    bool
	}
)

func probe(mode string) error {
	output := json.NewEncoder(os.Stdout)
	switch mode {
	case "inspect":
		status, err := os.ReadFile("/proc/self/status")
		if err != nil {
			return err
		}
		interfaces, err := net.Interfaces()
		if err != nil {
			return err
		}
		v4, err := os.ReadFile("/proc/self/net/route")
		if err != nil {
			return err
		}
		v6, err := os.ReadFile("/proc/self/net/ipv6_route")
		if err != nil {
			return err
		}
		network, err := os.Readlink("/proc/self/ns/net")
		if err != nil {
			return err
		}

		state, err := os.CreateTemp("/tmp/playground", ".probe-*")
		if err == nil {
			_ = state.Close()
			_ = os.Remove(state.Name())
		}

		return output.Encode(map[string]any{"status": string(status), "interfaces": interfaces,
			"ipv4": string(v4), "ipv6": string(v6), "network": network, "stateWriteDenied": errors.Is(err, os.ErrPermission)})
	case "connect":
		var requests []probeRequest
		if err := json.NewDecoder(os.Stdin).Decode(&requests); err != nil {
			return err
		}
		results := make([]probeResult, 0, len(requests))
		for _, request := range requests {
			result, err := connect(request)
			if err != nil {
				return err
			}
			results = append(results, result)
		}

		return output.Encode(results)
	case "canary":
		return loopbackCanary(output)
	default:
		return errors.New("unknown test probe")
	}
}

func connect(request probeRequest) (probeResult, error) {
	conn, err := net.DialTimeout(request.Network, request.Address, 800*time.Millisecond)
	if err != nil {
		return probeResult{Denied: errors.Is(err, os.ErrPermission)}, nil
	}
	defer conn.Close()
	result := probeResult{Connected: true}
	if request.Payload == "" {
		return result, nil
	}
	_ = conn.SetDeadline(time.Now().Add(7 * time.Second))
	if request.Network == "udp" {
		// A DNS A query for the approved fixture hostname. UDP connect alone
		// cannot establish whether the datagram reached the resolver.
		query := []byte("\x12\x34\x01\x00\x00\x01\x00\x00\x00\x00\x00\x00\x07mockery\x0aferretlang\x03org\x00\x00\x01\x00\x01")
		if _, err := conn.Write(query); err != nil {
			return result, nil
		}
		_ = conn.SetReadDeadline(time.Now().Add(800 * time.Millisecond))
		var reply [512]byte
		if n, err := conn.Read(reply[:]); err == nil && n > 12 && reply[0] == 0x12 && reply[1] == 0x34 {
			result.Reply = "dns"
			if reply[6] == 0 && reply[7] == 1 && n >= 16 {
				result.Reply = net.IP(reply[n-4 : n]).String()
			}
		}

		return result, nil
	}
	if _, err := io.WriteString(conn, request.Payload); err != nil {
		return result, err
	}
	reply, err := bufio.NewReader(io.LimitReader(conn, 8192)).ReadString('\n')
	if err != nil {
		return result, err
	}
	result.Reply = strings.TrimSpace(reply)

	return result, nil
}

func loopbackCanary(output *json.Encoder) error {
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return err
	}
	defer listener.Close()
	var connections atomic.Int32
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			connections.Add(1)
			_, _ = io.WriteString(conn, "canary\n")
			_ = conn.Close()
		}
	}()
	if err := output.Encode(listener.Addr().(*net.TCPAddr).Port); err != nil {
		return err
	}
	commands := bufio.NewScanner(os.Stdin)
	for commands.Scan() {
		if commands.Text() != "count" {
			return errors.New("unknown canary command")
		}
		if err := output.Encode(connections.Load()); err != nil {
			return err
		}
	}

	return commands.Err()
}
