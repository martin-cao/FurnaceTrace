#!/bin/sh
set -eu

context=${FURNACE_TRACE_KUBE_CONTEXT:-}
kubeconfig=${FURNACE_TRACE_KUBECONFIG:-}
if [ -z "$context" ]; then
    echo 'Set FURNACE_TRACE_KUBE_CONTEXT to the Kubernetes context for this project.' >&2
    exit 2
fi

if [ -n "$kubeconfig" ]; then
    exec kubectl --kubeconfig "$kubeconfig" --context "$context" --namespace furnace-trace "$@"
fi
exec kubectl --context "$context" --namespace furnace-trace "$@"
