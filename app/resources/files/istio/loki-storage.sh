#!/usr/bin/env bash

# Retain an existing StatefulSet's storage type: volumeClaimTemplates is immutable.
loki_storage_mode() {
  local existing default_class
  existing=$(kubectl get statefulset loki -n istio-system --ignore-not-found -o 'jsonpath={.metadata.name}{":"}{.spec.volumeClaimTemplates[0].metadata.name}') || return $?
  case "$existing" in
    loki:storage) printf persistent; return;;
    loki:) printf ephemeral; return;;
    ''|:) ;;
    *) fail "Unsupported existing Loki storage layout: $existing";;
  esac
  default_class=$(kubectl get storageclass -o go-template='{{range .items}}{{if .metadata.annotations}}{{if eq (index .metadata.annotations "storageclass.kubernetes.io/is-default-class") "true"}}{{.metadata.name}}{{"\n"}}{{end}}{{end}}{{end}}') || return $?
  if [[ -n $default_class ]]; then printf persistent; else printf ephemeral; fi
}

render_loki_manifest() {
  local mode=$1 source=$2 target=$3
  if [[ $mode == persistent ]]; then
    cp "$source" "$target"
  elif [[ $mode == ephemeral ]]; then
    awk '/^  volumeClaimTemplates:/ { exit } { print } /^      volumes:/ { print "        - name: storage\n          emptyDir: {}" }' "$source" > "$target"
  else
    fail "Unsupported Loki storage mode: $mode"
  fi
}
