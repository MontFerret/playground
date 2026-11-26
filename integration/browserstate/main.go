// Command browserstate inspects the private relay from the packaged test suite.
package main

import (
	"context"
	"encoding/json"
	"log"
	"net/url"
	"os"
	"time"

	"github.com/mafredri/cdp"
	"github.com/mafredri/cdp/devtool"
	"github.com/mafredri/cdp/protocol/browser"
	"github.com/mafredri/cdp/protocol/runtime"
	"github.com/mafredri/cdp/protocol/target"
	"github.com/mafredri/cdp/rpcc"
)

type observedTarget struct {
	ID      target.ID          `json:"id"`
	Type    string             `json:"type"`
	URL     string             `json:"url"`
	Context *browser.ContextID `json:"context"`
}

func main() {
	if len(os.Args) == 3 && os.Args[1] == "probe" {
		if err := probe(os.Args[2]); err != nil {
			log.Fatal(err)
		}
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
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
	if len(os.Args) == 3 && os.Args[1] == "evaluate" {
		if err := evaluate(ctx, client, version.WebSocketDebuggerURL, os.Args[2]); err != nil {
			log.Fatal(err)
		}
		return
	}

	processes, err := client.SystemInfo.GetProcessInfo(ctx)
	if err != nil {
		log.Fatal(err)
	}
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
		observed = append(observed, observedTarget{info.TargetID, info.Type, info.URL, info.BrowserContextID})
	}

	if err := json.NewEncoder(os.Stdout).Encode(map[string]any{"contexts": contexts.BrowserContextIDs, "targets": observed, "processes": processes.ProcessInfo}); err != nil {
		log.Fatal(err)
	}
}

// evaluate uses the private test observer. A new context uses Chromium's normal
// proxy, managed policy, and sandbox, without installing CDP Fetch interception.
func evaluate(ctx context.Context, client *cdp.Client, endpoint, pageID string) error {
	if pageID == "new" {
		created, err := client.Target.CreateBrowserContext(ctx, target.NewCreateBrowserContextArgs().SetDisposeOnDetach(true))
		if err != nil {
			return err
		}

		defer func() {
			cleanup, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			_ = client.Target.DisposeBrowserContext(cleanup, target.NewDisposeBrowserContextArgs(created.BrowserContextID))
		}()
		page, err := client.Target.CreateTarget(ctx, target.NewCreateTargetArgs("about:blank").SetBrowserContextID(created.BrowserContextID))
		if err != nil {
			return err
		}

		pageID = string(page.TargetID)
	}

	address, err := url.Parse(endpoint)
	if err != nil {
		return err
	}

	address.Path = "/devtools/page/" + pageID
	conn, err := rpcc.DialContext(ctx, address.String())
	if err != nil {
		return err
	}
	defer conn.Close()

	var expression string
	if err := json.NewDecoder(os.Stdin).Decode(&expression); err != nil {
		return err
	}

	page := cdp.NewClient(conn)
	value, err := page.Runtime.Evaluate(ctx, runtime.NewEvaluateArgs(expression).SetAwaitPromise(true).SetReturnByValue(true))
	if err != nil {
		return err
	}
	if value.ExceptionDetails != nil {
		return value.ExceptionDetails
	}

	return json.NewEncoder(os.Stdout).Encode(value.Result.Value)
}
