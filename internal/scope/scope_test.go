/*
Copyright 2026 Hyperbolic Labs, Inc.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package scope

import (
	"slices"
	"testing"
)

func TestParseNamespaces(t *testing.T) {
	for _, tc := range []struct {
		name, flag string
		want       []string
	}{
		{"empty watches everything", "", nil},
		{"one namespace", "ml", []string{"ml"}},
		{"trims, drops blanks and duplicates", " ml, research,,ml ", []string{"ml", "research"}},
	} {
		if got := ParseNamespaces(tc.flag); !slices.Equal(got, tc.want) {
			t.Errorf("%s: ParseNamespaces(%q) = %q, want %q", tc.name, tc.flag, got, tc.want)
		}
	}
}
