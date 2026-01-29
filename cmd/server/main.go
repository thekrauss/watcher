package main

import (
	"context"
	"os"

	"github.com/thekrauss/watcher/internal/kubernetes"
	"github.com/thekrauss/watcher/internal/restate"
)

func main() {
	ctx := context.Background()

	restateUrl := os.Getenv("RESTATE_URL")
	if restateUrl == "" {
		restateUrl = "http://localhost:8081"
	}

	rc := restate.NewRestateClient(restateUrl)
	kubernetes.WatchPods(ctx, rc)
}
