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

# Prints the chart's artifacthub.io/changes for a release of HEAD: one entry
# per pull request merged since LAST, the last release, or since the start
# when LAST is empty. Usage: hack/chart-changes.sh [LAST]
#
# Pull requests are squash-merged, so each commit subject is a pull request
# title, a Conventional Commit. Artifact Hub lists features as added, fixes as
# fixed, and performance and breaking changes as changed. Other types, such as
# docs, ci and chore, change nothing a chart user sees, and are left out.
# Prints nothing when no title qualifies.

set -euo pipefail

last=${1:-}
repo=${GITHUB_REPOSITORY:-HyperbolicLabs/rental-operator}

title='^([a-z]+)(\([^)]*\))?(!)?: (.+)$'
numbered='^(.*) \(#([0-9]+)\)$'

git log --reverse --format=%s ${last:+"$last..HEAD"} | while IFS= read -r subject; do
	[[ $subject =~ $title ]] || continue
	type=${BASH_REMATCH[1]} breaking=${BASH_REMATCH[3]} text=${BASH_REMATCH[4]}
	case "$type" in
	feat) kind=added ;;
	fix) kind=fixed ;;
	perf) kind=changed ;;
	*) kind= ;;
	esac
	[ -n "$breaking" ] && kind=changed
	[ -n "$kind" ] || continue

	pr=
	if [[ $text =~ $numbered ]]; then
		text=${BASH_REMATCH[1]} pr=${BASH_REMATCH[2]}
	fi
	# Titles are lowercase for the commit log. Entries read as sentences.
	text=$(tr '[:lower:]' '[:upper:]' <<<"${text:0:1}")${text:1}
	text=${text//\\/\\\\}
	text=${text//\"/\\\"}

	printf -- '- kind: %s\n  description: "%s"\n' "$kind" "$text"
	if [ -n "$pr" ]; then
		printf -- '  links:\n    - name: "Pull request #%s"\n      url: https://github.com/%s/pull/%s\n' "$pr" "$repo" "$pr"
	fi
done
