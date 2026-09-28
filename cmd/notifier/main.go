package main

import (
	"furnace.local/iot/internal/notifier"
	"furnace.local/iot/internal/platform"
	"log"
)

func main() {
	ctx, cancel := platform.Context()
	defer cancel()
	if err := notifier.Run(ctx); err != nil {
		log.Fatal(err)
	}
}
