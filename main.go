package main

import (
	"math/rand"
	"time"

	"github.com/badal-io/devpod-provider-gcloud/cmd"
)

func main() {
	rand.Seed(time.Now().UTC().UnixNano())
	cmd.Execute()
}
