// Command browserstate inspects the private relay from the packaged test suite.
package main

import (
	"context"
	"encoding/json"
	"log"
	"os"
	"time"

	"github.com/mafredri/cdp"
	"github.com/mafredri/cdp/devtool"
	"github.com/mafredri/cdp/protocol/browser"
	"github.com/mafredri/cdp/protocol/target"
	"github.com/mafredri/cdp/rpcc"
)

type observedTarget struct {
	ID      target.ID          `json:"id"`
	Type    string             `json:"type"`
	Context *browser.ContextID `json:"context"`
}

func main() {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	version, err := devtool.New("http://127.0.0.1:9223").Version(ctx)
	if err != nil {
		log.Fatal(err)
	}

	conn, err := rpcc.DialContext(ctx, version.WebSocketDebuggerURL)
	if err != nil {
		log.Fatal(err)
	}
	defer conn.Close()

	client := cdp.NewClient(conn)
	contexts, err := client.Target.GetBrowserContexts(ctx)
	if err != nil {
		log.Fatal(err)
	}

	targets, err := client.Target.GetTargets(ctx, target.NewGetTargetsArgs())
	if err != nil {
		log.Fatal(err)
	}

	observed := make([]observedTarget, 0, len(targets.TargetInfos))
	for _, info := range targets.TargetInfos {
		observed = append(observed, observedTarget{info.TargetID, info.Type, info.BrowserContextID})
	}

	if err := json.NewEncoder(os.Stdout).Encode(map[string]any{"contexts": contexts.BrowserContextIDs, "targets": observed}); err != nil {
		log.Fatal(err)
	}
}
