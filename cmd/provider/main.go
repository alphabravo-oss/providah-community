package main

import (
	"github.com/alphabravo-oss/providah-community/sdk/provider"
	"github.com/alphabravo-oss/providah-community/worker"
)

var version = "development"

func main() { worker.Run(version, worker.Power, provider.CommunityRuntime) }
