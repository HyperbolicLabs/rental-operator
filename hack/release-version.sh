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

# Prints the version the next release gets on stdout.
# Usage: hack/release-version.sh [VERSION]
#
# The suggestion follows from the pull request titles merged since the last
# release, which are Conventional Commits. A breaking change, marked with "!"
# or a BREAKING CHANGE footer, bumps the minor version before 1.0 and the
# major version after. A feature bumps the minor version, and anything else
# the patch version. A VERSION, when given, must not be smaller than the
# suggestion. It may be a pre-release of it, such as v0.3.0-rc.1, or a larger
# version.

set -euo pipefail

requested=${1:-}

die() {
	echo "release-version: $*" >&2
	exit 1
}

if [ -n "$requested" ] && ! [[ $requested =~ ^v[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.-]+)?$ ]]; then
	die "$requested is not a version like v1.2.3 or v1.2.3-rc.1"
fi
if [ -n "$requested" ] && git rev-parse -q --verify "refs/tags/$requested" >/dev/null; then
	die "$requested is already released"
fi

# The last release, not counting pre-releases.
last=$(git tag --list 'v[0-9]*' --merged HEAD | grep -v -- - | sort -V | tail -n1 || true)
if [ -z "$last" ]; then
	suggested=v0.1.0
else
	subjects=$(git log --format=%s "$last..HEAD")
	[ -n "$subjects" ] || die "nothing to release since $last"
	IFS=. read -r major minor patch <<<"${last#v}"
	if grep -qE '^[a-z]+(\([^)]*\))?!:' <<<"$subjects" || git log --format=%b "$last..HEAD" | grep -qE '^BREAKING[ -]CHANGE:'; then
		if [ "$major" = 0 ]; then
			minor=$((minor + 1)) patch=0
		else
			major=$((major + 1)) minor=0 patch=0
		fi
	elif grep -qE '^feat(\([^)]*\))?:' <<<"$subjects"; then
		minor=$((minor + 1)) patch=0
	else
		patch=$((patch + 1))
	fi
	suggested=v$major.$minor.$patch
fi
echo "Suggested version: $suggested" >&2

if [ -z "$requested" ]; then
	echo "$suggested"
	exit 0
fi
# sort -V orders a pre-release after its release (v0.3.0 before v0.3.0-rc.1),
# so a pre-release of the suggested version passes, as it should.
if [ "$(printf '%s\n%s\n' "$suggested" "$requested" | sort -V | head -n1)" != "$suggested" ]; then
	die "$requested is smaller than $suggested, the smallest version the changes since ${last:-the start} allow"
fi
echo "$requested"
