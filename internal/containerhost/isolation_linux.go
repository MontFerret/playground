package containerhost

import (
	"bytes"
	"errors"
	"io"
	"log/slog"
	"net"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"

	"golang.org/x/sys/unix"
)

const maxInterfaceBytes = 64 << 10

func isolateNetwork() error {
	if err := checkNetworkNamespace(os.NewFile(5, "parent-network")); err != nil {
		return err
	}

	interfaces, netlinkErr := net.Interfaces()
	names := make([]string, len(interfaces))
	for i, iface := range interfaces {
		names[i] = iface.Name
	}
	procNames, procErr := readNetworkInterfaces()
	slog.Info("Container network inspection",
		"netlinkOK", netlinkErr == nil, "netlinkErrno", errorNumber(netlinkErr),
		"netlinkInterfaceCount", len(names), "netlinkOnlyLoopback", onlyLoopback(names),
		"procfsOK", procErr == nil, "procfsErrno", errorNumber(procErr),
		"procfsInterfaceCount", len(procNames), "procfsOnlyLoopback", onlyLoopback(procNames))
	// Netlink remains authoritative; procfs supplies independent diagnostics.
	if netlinkErr != nil {
		return isolationFailure("network_interface_enumeration_failed", netlinkErr)
	}

	if !onlyLoopback(names) {
		return isolationFailure("network_interface_layout_invalid", nil)
	}

	fd, err := unix.Socket(unix.AF_INET, unix.SOCK_DGRAM|unix.SOCK_CLOEXEC, 0)
	if err != nil {
		return isolationFailure("loopback_socket_failed", err)
	}
	defer unix.Close(fd)
	request, err := unix.NewIfreq("lo")
	if err != nil {
		return isolationFailure("loopback_request_failed", err)
	}
	request.SetUint16(unix.IFF_UP)
	if err := unix.IoctlIfreq(fd, unix.SIOCSIFFLAGS, request); err != nil {
		return isolationFailure("loopback_configuration_failed", err)
	}

	cmd := exec.Command("nft", "-f", "-")
	cmd.Stdin = strings.NewReader(`add table inet playground
add chain inet playground output { type filter hook output priority -150; policy accept; }
add rule inet playground output meta skuid 10001 ip daddr 127.0.0.1 tcp dport 3128 accept
add rule inet playground output meta skuid 10001 ip daddr 127.0.0.1 tcp sport 9223 ct direction reply ct state established accept
add rule inet playground output meta skuid 10001 drop
`)
	if err := cmd.Run(); err != nil {
		return isolationFailure("namespace_policy_unavailable", err)
	}

	return nil
}

// checkNetworkNamespace consumes the inherited descriptor before any helper starts.
func checkNetworkNamespace(parent *os.File) error {
	defer parent.Close()
	outer, err := parent.Stat()
	if err != nil {
		return isolationFailure("parent_network_namespace_inspection_failed", err)
	}

	inner, err := os.Stat("/proc/self/ns/net")
	if err != nil {
		return isolationFailure("network_namespace_inspection_failed", err)
	}

	distinct := !os.SameFile(outer, inner)
	slog.Info("Container network namespace inspection", "distinct", distinct)
	if !distinct {
		return isolationFailure("network_namespace_shared", nil)
	}

	return nil
}

func readNetworkInterfaces() ([]string, error) {
	file, err := os.Open("/proc/self/net/dev")
	if err != nil {
		return nil, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, maxInterfaceBytes+1))
	if err != nil {
		return nil, err
	}

	return parseNetworkInterfaces(data)
}

// procfs reports every device in this process's network namespace, including
// interfaces without addresses. Bound and validate the complete kernel record.
func parseNetworkInterfaces(data []byte) ([]string, error) {
	invalid := errors.New("network_interface_data_invalid")
	if len(data) > maxInterfaceBytes || !bytes.HasSuffix(data, []byte("\n")) {
		return nil, invalid
	}

	lines := strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
	if len(lines) < 3 || strings.Join(strings.Fields(lines[0]), " ") != "Inter-| Receive | Transmit" {
		return nil, invalid
	}

	header := strings.Split(lines[1], "|")
	if len(header) != 3 || strings.TrimSpace(header[0]) != "face" ||
		strings.Join(strings.Fields(header[1]), " ") != "bytes packets errs drop fifo frame compressed multicast" ||
		strings.Join(strings.Fields(header[2]), " ") != "bytes packets errs drop fifo colls carrier compressed" {
		return nil, invalid
	}

	var names []string
	seen := make(map[string]bool)
	for _, line := range lines[2:] {
		name, counters, ok := strings.Cut(line, ":")
		name = strings.TrimSpace(name)
		if !ok || name == "" || len(name) > 15 || len(strings.Fields(name)) != 1 || seen[name] {
			return nil, invalid
		}
		seen[name] = true
		fields := strings.Fields(counters)
		if len(fields) != 16 {
			return nil, invalid
		}

		for _, field := range fields {
			if _, err := strconv.ParseUint(field, 10, 64); err != nil {
				return nil, invalid
			}
		}
		names = append(names, name)
	}

	return names, nil
}

func onlyLoopback(names []string) bool {
	return len(names) == 1 && names[0] == "lo"
}

func errorNumber(err error) int {
	var errno syscall.Errno
	if errors.As(err, &errno) {
		return int(errno)
	}

	return 0
}

func isolationFailure(stage string, err error) error {
	status := 0
	var exited *exec.ExitError
	if errors.As(err, &exited) {
		status = exited.ExitCode()
	}
	slog.Error("Container isolation failed", "stage", stage, "errno", errorNumber(err), "exitStatus", status)

	return errors.New(stage)
}
