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

package account

import "sync"

// CredentialsMemory remembers, in memory only, the last Credentials resolved
// for each config. When a namespace is deleted, its Secret can disappear
// before the GPURentals that depend on it. The remembered key lets the
// operator still terminate those rentals instead of leaking them. It is used
// for nothing else, and it is empty after the operator restarts.
type CredentialsMemory struct {
	mu   sync.Mutex
	keys map[string]Credentials
}

// NewCredentialsMemory returns an empty CredentialsMemory.
func NewCredentialsMemory() *CredentialsMemory {
	return &CredentialsMemory{keys: map[string]Credentials{}}
}

// Remember records the credentials config resolved to.
func (m *CredentialsMemory) Remember(config string, c Credentials) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.keys[config] = c
}

// Recall returns the last credentials config resolved to, if any.
func (m *CredentialsMemory) Recall(config string) (Credentials, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	c, ok := m.keys[config]
	return c, ok
}
