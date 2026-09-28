package main

import (
	"furnace.local/iot/internal/platform"
	"furnace.local/iot/internal/scada"
	"log"
)

func main() {
	ctx, cancel := platform.Context()
	defer cancel()
	if err := scada.Run(ctx); err != nil {
		log.Fatal(err)
	}
}
