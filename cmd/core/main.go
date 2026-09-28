package main

import (
	"furnace.local/iot/internal/core"
	"furnace.local/iot/internal/platform"
	"log"
)

func main() {
	ctx, cancel := platform.Context()
	defer cancel()
	if err := core.Run(ctx); err != nil {
		log.Fatal(err)
	}
}
