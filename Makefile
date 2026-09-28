.PHONY: api build up down demo test
api:
	uv run --with openapi-spec-validator python -m openapi_spec_validator api/openapi.yaml
build:
	sh scripts/build.sh
up:
	sh scripts/up.sh
down:
	sh scripts/down.sh
demo:
	python3 scripts/demo.py success
# Run only when changing the command model, scanner stability, or notification delivery.
test:
	go test ./internal/device ./internal/vision ./internal/notifier
