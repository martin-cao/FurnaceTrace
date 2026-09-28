FROM golang:1.26-alpine AS builder
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
COPY internal ./internal
COPY api ./api
ARG SERVICE
RUN --mount=type=cache,target=/root/.cache/go-build CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' -o /out/furnace-trace-${SERVICE} ./cmd/${SERVICE}

FROM alpine:3.22 AS runtime
ARG SERVICE
ENV FURNACE_TRACE_SERVICE=${SERVICE}
RUN apk add --no-cache ca-certificates
COPY --from=builder /out/furnace-trace-${SERVICE} /usr/local/bin/
USER 65532:65532
ENTRYPOINT ["/bin/sh", "-c", "exec /usr/local/bin/furnace-trace-${FURNACE_TRACE_SERVICE}"]

FROM runtime AS media
USER root
RUN apk add --no-cache ffmpeg
USER 65532:65532
