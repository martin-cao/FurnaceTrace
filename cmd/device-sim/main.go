package main

import (
	"furnace.local/iot/internal/device"
	"furnace.local/iot/internal/platform"
	"log"
)

func main() {
	ctx, cancel := platform.Context()
	defer cancel()
	if err := device.Run(ctx); err != nil {
		log.Fatal(err)
	}
}
