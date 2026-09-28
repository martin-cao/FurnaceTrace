package main

import (
	"furnace.local/iot/internal/mes"
	"furnace.local/iot/internal/platform"
	"log"
)

func main() {
	ctx, cancel := platform.Context()
	defer cancel()
	if err := mes.Run(ctx); err != nil {
		log.Fatal(err)
	}
}
