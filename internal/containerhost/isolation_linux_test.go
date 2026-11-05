package containerhost

import (
	"bytes"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"testing"
)

func TestNetworkInterfaceData(t *testing.T) {
	valid := networkDeviceData("lo")
	for _, tc := range []struct {
		name, data      string
		valid, loopback bool
	}{
		{"loopback", valid, true, true},
		{"external", networkDeviceData("lo", "eth0"), true, false},
		{"other_only", networkDeviceData("eth0"), true, false},
		{"missing", "", false, false},
		{"no_devices", networkDeviceData(), false, false},
		{"duplicate", networkDeviceData("lo", "lo"), false, false},
		{"truncated", strings.TrimSuffix(valid, "\n"), false, false},
		{"header", strings.Replace(valid, "Receive", "private-header", 1), false, false},
		{"counters", strings.Replace(valid, "0 0 0", "0 invalid 0", 1), false, false},
		{"short_record", strings.Replace(valid, "0 0 0", "0 0", 1), false, false},
		{"overflow", strings.Replace(valid, "0 0 0", "0 18446744073709551616 0", 1), false, false},
		{"malformed_tail", valid + "private-tail\n", false, false},
		{"oversized", valid + strings.Repeat(" ", maxInterfaceBytes) + "\n", false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			names, err := parseNetworkInterfaces([]byte(tc.data))
			if (err == nil) != tc.valid || onlyLoopback(names) != tc.loopback {
				t.Fatalf("valid=%v, loopback=%v", err == nil, onlyLoopback(names))
			}

			if err != nil && err.Error() != "network_interface_data_invalid" {
				t.Fatal("interface data escaped its fixed error code")
			}
		})
	}
}

func TestNetworkNamespaceRejectsSharedAndClosesDescriptor(t *testing.T) {
	parent, err := os.Open("/proc/self/ns/net")
	if err != nil {
		t.Fatal(err)
	}

	if err := checkNetworkNamespace(parent); err == nil || err.Error() != "network_namespace_shared" {
		t.Fatalf("shared namespace: %v", err)
	}

	if _, err := parent.Stat(); !errors.Is(err, os.ErrClosed) {
		t.Fatal("namespace descriptor was not closed")
	}

	if err := checkNetworkNamespace(parent); err == nil || err.Error() != "parent_network_namespace_inspection_failed" {
		t.Fatalf("unavailable namespace: %v", err)
	}
}

func TestIsolationDiagnosticsAreSanitized(t *testing.T) {
	var output bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&output, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })
	err := isolationFailure("network_interface_enumeration_failed", fmt.Errorf("private-request-value: %w", syscall.EPERM))
	if err.Error() != "network_interface_enumeration_failed" ||
		!strings.Contains(output.String(), `"errno":1`) || strings.Contains(output.String(), "private-request-value") {
		t.Fatal("isolation diagnostics lost errno or leaked raw error data")
	}

	err = exec.Command("/bin/sh", "-c", "exit 23").Run()
	if err == nil {
		t.Fatal("exit fixture succeeded")
	}
	_ = isolationFailure("namespace_policy_unavailable", err)
	if !strings.Contains(output.String(), `"exitStatus":23`) {
		t.Fatal("child exit status was lost")
	}
}

func networkDeviceData(names ...string) string {
	data := "Inter-|   Receive | Transmit\n" +
		" face |bytes packets errs drop fifo frame compressed multicast|bytes packets errs drop fifo colls carrier compressed\n"
	for _, name := range names {
		data += name + ": " + strings.TrimSpace(strings.Repeat("0 ", 16)) + "\n"
	}

	return data
}
