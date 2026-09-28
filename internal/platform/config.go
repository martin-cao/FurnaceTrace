package platform

import (
	"encoding/json"
	"fmt"
	"os"
)

type FurnaceConfig struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Sensor      string `json:"sensor"`
	Door        string `json:"door"`
	Lamp        string `json:"lamp"`
	Camera      string `json:"camera"`
	VisionURL   string `json:"visionUrl"`
	PreviewURL  string `json:"previewUrl"`
	Temperature string `json:"temperature"`
}

func (f FurnaceConfig) DeviceIDs() map[string]string {
	devices := map[string]string{"sensor": f.Sensor, "door": f.Door, "lamp": f.Lamp}
	if f.Temperature != "" {
		devices["temperature"] = f.Temperature
	}
	return devices
}

func Config() ([]FurnaceConfig, error) {
	b, err := os.ReadFile(Env("FURNACES_CONFIG", "configs/furnaces.json"))
	if err != nil {
		return nil, err
	}
	var cfg []FurnaceConfig
	if err = json.Unmarshal(b, &cfg); err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	for _, f := range cfg {
		if f.ID == "" || f.Sensor == "" || f.Door == "" || f.Lamp == "" || f.VisionURL == "" || seen[f.ID] {
			return nil, fmt.Errorf("invalid furnace configuration")
		}
		seen[f.ID] = true
	}
	if len(cfg) == 0 {
		return nil, fmt.Errorf("at least one furnace is required")
	}
	return cfg, nil
}
