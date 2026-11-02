// Command browsercheck validates the running Chromium renderer sandbox.
package main

import (
	"context"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/mafredri/cdp"
	"github.com/mafredri/cdp/devtool"
	"github.com/mafredri/cdp/rpcc"
)

func main() {
	if !healthy() {
		os.Exit(1)
	}
}

func healthy() bool {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	version, err := devtool.New("http://127.0.0.1:9223").Version(ctx)
	if err != nil {
		return false
	}
	conn, err := rpcc.DialContext(ctx, version.WebSocketDebuggerURL)
	if err != nil {
		return false
	}
	defer conn.Close()
	processes, err := cdp.NewClient(conn).SystemInfo.GetProcessInfo(ctx)
	if err != nil {
		return false
	}
	baseline, err := os.ReadFile("/proc/self/status")
	if err != nil {
		return false
	}
	outer := fields(string(baseline))
	found := false
	for _, process := range processes.ProcessInfo {
		if process.Type != "renderer" {
			continue
		}
		status, err := os.ReadFile("/proc/" + strconv.FormatInt(int64(process.ID), 10) + "/status")
		if err != nil {
			return false
		}
		renderer := fields(string(status))
		filters, _ := strconv.Atoi(renderer["Seccomp_filters"])
		base, _ := strconv.Atoi(outer["Seccomp_filters"])
		if renderer["Seccomp"] == "2" && filters > base &&
			len(strings.Fields(renderer["NSpid"])) > len(strings.Fields(outer["NSpid"])) &&
			strings.HasPrefix(renderer["Uid"], "10001\t") {
			found = true
		} else {
			return false
		}
	}
	return found
}

func fields(status string) map[string]string {
	result := make(map[string]string)
	for line := range strings.SplitSeq(status, "\n") {
		key, value, ok := strings.Cut(line, ":")
		if ok {
			result[key] = strings.TrimSpace(value)
		}
	}
	return result
}
