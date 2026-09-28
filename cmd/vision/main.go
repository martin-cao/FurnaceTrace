package main

import (
	"furnace.local/iot/internal/platform"
	"furnace.local/iot/internal/vision"
	"log"
)

func main() {
	ctx, cancel := platform.Context()
	defer cancel()
	if err := vision.Run(ctx); err != nil {
		log.Fatal(err)
	}
}
