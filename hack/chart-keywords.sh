#!/usr/bin/env bash
# Copyright 2026 Hyperbolic Labs, Inc.
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

# Rewrites the GPU types among the chart's keywords from the published API
# spec, so Artifact Hub searches for a GPU type find the chart.
# Usage: hack/chart-keywords.sh [SPEC_URL]
#
# The types are the values the spec lists for gpuType when creating a rental.
# Only the lines between the two marker comments in Chart.yaml change. The
# script fails rather than empty the list when the spec lists no types.

set -euo pipefail

spec_url=${1:-https://api.hyperbolic.ai/v2/openapi.json}
chart=charts/rental-operator/Chart.yaml
begin='  # GPU types: `make chart-keywords` rewrites the lines up to the next comment.'
end='  # End of GPU types.'

types=$(curl -fsSL "$spec_url" | jq -r '
  .paths["/v2/on-demand/rentals"].post.requestBody.content["application/json"].schema.properties.gpuType
  | (.enum // .examples // .["x-extensible-enum"] // [])[]')
if [ -z "$types" ]; then
  echo "No GPU types found for gpuType in $spec_url." >&2
  exit 1
fi
if ! grep -qxF "$begin" "$chart" || ! grep -qxF "$end" "$chart"; then
  echo "The GPU type markers are missing from $chart." >&2
  exit 1
fi

TYPES=$types BEGIN=$begin END=$end awk '
  $0 == ENVIRON["BEGIN"] {
    print
    n = split(ENVIRON["TYPES"], t, "\n")
    for (i = 1; i <= n; i++) print "  - " t[i]
    skip = 1
    next
  }
  $0 == ENVIRON["END"] { skip = 0 }
  !skip { print }
' "$chart" > "$chart.tmp"
mv "$chart.tmp" "$chart"
