#!/usr/bin/env bash
source "$(dirname "$0")/../../common.sh"
require_root "$@"
validate_version "$1"
images=$(kubeadm config images list --kubernetes-version "$1" --image-repository "$3")
local_images=$(ctr -n k8s.io images list -q)
while IFS= read -r image; do
  [[ -n $image ]] || continue
  if grep -Fxq -- "$image" <<< "$local_images"; then
    printf 'Image already exists locally, skipping pull: %s\n' "$image"
    continue
  fi
  printf 'Pulling missing image: %s\n' "$image"
  ctr -n k8s.io images pull "$image"
  local_images+=$'\n'"$image"
done <<< "$images"
