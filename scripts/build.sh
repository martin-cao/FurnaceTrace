#!/bin/sh
set -eu

root=$(CDPATH= cd "$(dirname "$0")/.." && pwd)
cd "$root"
build_tag=$(date -u '+%Y%m%dT%H%M%SZ')
image_prefix=${FURNACE_TRACE_IMAGE_PREFIX:-furnace-trace-}
image_platform=${FURNACE_TRACE_IMAGE_PLATFORM:-linux/amd64}
push_prefix=${FURNACE_TRACE_IMAGE_PREFIX:-}

if [ "$#" -eq 0 ]; then
    set -- core scada-adapter notifier mes device-sim vision camera-sim qr-display
    fuxa_image="${image_prefix}fuxa:${build_tag}"
    echo 'Building FUXA'
    if [ -n "$push_prefix" ]; then
        docker buildx build --quiet --platform "$image_platform" --push -t "$fuxa_image" -f deploy/fuxa/Dockerfile deploy/fuxa >/dev/null
    else
        docker build --quiet -t "$fuxa_image" -f deploy/fuxa/Dockerfile deploy/fuxa >/dev/null
    fi
    python3 scripts/record_image.py fuxa "$fuxa_image"
fi

for service in "$@"; do
    case "$service" in
        core|qr-display)
            if [ ! -d web/node_modules ]; then
                npm ci --prefix web --no-audit --no-fund
            fi
            python3 scripts/embed_web.py
            ;;
    esac

    target=runtime
    case "$service" in
        vision|camera-sim) target=media ;;
    esac

    image="${image_prefix}${service}:${build_tag}"
    echo "Building $service"
    if [ -n "$push_prefix" ]; then
        docker buildx build --quiet --platform "$image_platform" --push --target "$target" --build-arg "SERVICE=$service" -t "$image" -f deploy/Dockerfile . >/dev/null
    else
        docker build --quiet --target "$target" --build-arg "SERVICE=$service" -t "$image" -f deploy/Dockerfile . >/dev/null
    fi
    python3 scripts/record_image.py "$service" "$image"
done
