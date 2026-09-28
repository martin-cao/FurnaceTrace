package main

import (
	"furnace.local/iot/internal/camera"
	"furnace.local/iot/internal/platform"
	"log"
)

func main() {
	ctx, cancel := platform.Context()
	defer cancel()
	if err := camera.Run(ctx); err != nil {
		log.Fatal(err)
	}
}
