package containerhost

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"os/exec"
	"slices"
	"time"
)

type (
	namespaceLink struct {
		Index         int               `json:"ifindex"`
		Name          string            `json:"ifname"`
		Type          string            `json:"link_type"`
		Flags         []string          `json:"flags"`
		State         string            `json:"operstate"`
		Master        *string           `json:"master"`
		Lower         *string           `json:"link"`
		LowerIndex    *int              `json:"link_index"`
		PeerNamespace *int              `json:"link_netnsid"`
		Addresses     []json.RawMessage `json:"addr_info"`
		Info          struct {
			Kind string `json:"info_kind"`
			Data struct {
				Local  string  `json:"local"`
				Remote string  `json:"remote"`
				Link   *string `json:"link"`
			} `json:"info_data"`
		} `json:"linkinfo"`
	}

	namespaceRoute struct {
		Destination   string          `json:"dst"`
		Type          string          `json:"type"`
		Device        string          `json:"dev"`
		NextHops      json.RawMessage `json:"nexthops"`
		NextHopID     json.RawMessage `json:"nhid"`
		Encapsulation json.RawMessage `json:"encap"`
		Gateway       json.RawMessage `json:"gateway"`
		Via           json.RawMessage `json:"via"`
	}
)

// Inspect the complete namespace before enabling loopback or starting any browser
// process. A separately enumerated link must appear in the detailed address dump.
func verifyNetworkLayout(ctx context.Context, interfaces []net.Interface) error {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()

	links, err := readIPInventory[namespaceLink](ctx, "-d", "address", "show")
	if err != nil {
		return isolationFailure("network_link_inspection_failed", err)
	}

	if !isolatedLinks(interfaces, links) {
		return isolationFailure("network_interface_layout_invalid", nil)
	}

	for _, family := range []string{"-4", "-6"} {
		routes, err := readIPInventory[namespaceRoute](ctx, family, "route", "show", "table", "all")
		if err != nil {
			return isolationFailure("network_route_inspection_failed", err)
		}

		for _, route := range routes {
			if !isolatedRoute(route) {
				return isolationFailure("network_route_layout_invalid", nil)
			}
		}
	}
	slog.Info("Container isolation verified", "stage", "network_layout", "inertTunnels", len(links)-1)

	return nil
}

func readIPInventory[T any](ctx context.Context, args ...string) ([]T, error) {
	// iproute2 can report interrupted dumps on stderr. Combining the streams
	// rejects warnings alongside otherwise valid but incomplete JSON.
	cmd := exec.CommandContext(ctx, "/usr/sbin/ip", append([]string{"-j"}, args...)...)
	output, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	cmd.Stderr = cmd.Stdout

	if err := cmd.Start(); err != nil {
		_ = output.Close()

		return nil, err
	}

	const limit = 1 << 20
	data, readErr := io.ReadAll(io.LimitReader(output, limit+1))
	_ = output.Close()
	waitErr := cmd.Wait()
	if err := errors.Join(readErr, waitErr); err != nil {
		return nil, err
	}

	var records []T
	if len(data) > limit || json.Unmarshal(data, &records) != nil || records == nil {
		return nil, errors.New("invalid_ip_inventory")
	}

	return records, nil
}

func isolatedLinks(interfaces []net.Interface, links []namespaceLink) bool {
	if len(links) != len(interfaces) {
		return false
	}

	expected := make(map[int]net.Interface, len(interfaces))
	for _, iface := range interfaces {
		expected[iface.Index] = iface
	}

	loopback := false
	for _, link := range links {
		iface, ok := expected[link.Index]
		if !ok || link.Name != iface.Name || link.Index <= 0 || link.Flags == nil || link.Addresses == nil ||
			link.Master != nil || link.Lower != nil || link.LowerIndex != nil || link.PeerNamespace != nil {
			return false
		}
		delete(expected, link.Index)

		if link.Name == "lo" {
			if loopback || link.Type != "loopback" || iface.Flags&net.FlagLoopback == 0 || !slices.Contains(link.Flags, "LOOPBACK") {
				return false
			}
			loopback = true

			continue
		}

		// Both tunnel families may have kernel-created wildcard fallback devices.
		// A bound endpoint or an underlying device makes this a different layout.
		kindOK := (link.Info.Kind == "ipip" && link.Type == "ipip") ||
			(link.Info.Kind == "ip6tnl" && link.Type == "tunnel6")
		if !kindOK || iface.Flags&(net.FlagUp|net.FlagRunning|net.FlagLoopback) != 0 ||
			link.State != "DOWN" || slices.Contains(link.Flags, "UP") || slices.Contains(link.Flags, "LOWER_UP") ||
			len(link.Addresses) != 0 || link.Info.Data.Local != "any" || link.Info.Data.Remote != "any" || link.Info.Data.Link != nil {
			return false
		}
	}

	return loopback && len(expected) == 0
}

func isolatedRoute(route namespaceRoute) bool {
	if route.Destination == "" || route.NextHops != nil || route.NextHopID != nil || route.Encapsulation != nil ||
		route.Gateway != nil || route.Via != nil || (route.Device != "" && route.Device != "lo") {
		return false
	}

	switch route.Type {
	case "unreachable", "blackhole", "prohibit", "throw":
		return true
	case "", "unicast", "local", "broadcast", "multicast", "anycast":
		return route.Device == "lo" && route.Destination != "default" &&
			route.Destination != "0.0.0.0/0" && route.Destination != "::/0"
	default:
		return false
	}
}
