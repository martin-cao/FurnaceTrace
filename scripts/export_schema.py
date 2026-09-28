"""Export MQTT JSON Schema from the canonical OpenAPI models."""
import json
from pathlib import Path
import yaml
root=Path(__file__).resolve().parents[1]
api=yaml.safe_load((root/"api/openapi.yaml").read_text())
models=api["components"]["schemas"]
schema={"$schema":"https://json-schema.org/draft/2020-12/schema","title":"Furnace MQTT messages",
    "$defs":{name:models[name] for name in ("DeviceState","DeviceCommand")},
    "oneOf":[{"$ref":"#/$defs/DeviceState"},{"$ref":"#/$defs/DeviceCommand"}]}
(root/"api/mqtt.schema.json").write_text(json.dumps(schema,ensure_ascii=False,indent=2)+"\n")
