/*
Copyright 2026.

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

// Package hyperbolic is a thin, hand-written client for the subset of the
// Hyperbolic public REST API the operator drives. It is modelled directly on
// the published contract (Bearer auth, /v2/on-demand/* and /v2/customer/*).
package hyperbolic

// Rental status values returned by the API (onDemandRentalStatusSchema).
// Any unrecognized value is coerced by the server to "Unknown".
const (
	StatusPending     = "Pending"
	StatusRunning     = "Running"
	StatusRebooting   = "Rebooting"
	StatusTerminating = "Terminating"
	StatusTerminated  = "Terminated"
	StatusFailed      = "Failed"
	StatusUnknown     = "Unknown"
)

// FeatureGuestReboot is the per-instance supportedFeatures member gating reboot.
const FeatureGuestReboot = "guest-reboot"

// Balance is GET /v2/customer/balance.
type Balance struct {
	BalanceCents      int64 `json:"balanceCents"`
	MaxOverdraftCents int64 `json:"maxOverdraftCents"`
}

// Node describes a machine shape inside a rental option or rental.
type Node struct {
	CPUModel  string `json:"cpuModel,omitempty"`
	GPUCount  int    `json:"gpuCount,omitempty"`
	RAMGb     int    `json:"ramGb,omitempty"`
	StorageGb int    `json:"storageGb,omitempty"`
	VCPUCount int    `json:"vcpuCount,omitempty"`
}

// RentalOption is one entry of GET /v2/on-demand/rental-options (unauthenticated
// marketplace catalogue). costPerHourCents is in CENTS.
type RentalOption struct {
	MachineType      string  `json:"machineType"`
	Enabled          bool    `json:"enabled"`
	CostPerHourCents float64 `json:"costPerHourCents"`
	Region           string  `json:"region"`
	GPUCount         int     `json:"gpuCount"`
	GPUType          string  `json:"gpuType"`
	GPUFormFactor    string  `json:"gpuFormFactor,omitempty"`
	ConnectionType   string  `json:"connectionType"`
	EthernetVariant  *string `json:"ethernetVariant,omitempty"`
	TotalAvailable   *int    `json:"totalAvailable,omitempty"`
	Nodes            []Node  `json:"nodes,omitempty"`
}

// SSHNetworking is a rental-level SSH endpoint.
type SSHNetworking struct {
	Host string `json:"host"`
	Port int    `json:"port"`
	User string `json:"user"`
}

// NodeNetworking is a rental-level public/private IP pair.
type NodeNetworking struct {
	PublicIP  string `json:"publicIp"`
	PrivateIP string `json:"privateIp"`
}

// PortForward is a NAT mapping (populated by port-forwarding providers only;
// forge leaves this empty and opens customPorts directly instead).
type PortForward struct {
	ExternalPort int `json:"externalPort"`
	InternalPort int `json:"internalPort"`
}

// ProviderData is the public, provider-agnostic networking/port view.
type ProviderData struct {
	SSHNetworking  []SSHNetworking  `json:"sshNetworking,omitempty"`
	NodeNetworking []NodeNetworking `json:"nodeNetworking,omitempty"`
	NetworkType    string           `json:"networkType,omitempty"`
	PortForwards   []PortForward    `json:"portForwards,omitempty"`
	CustomPorts    []int            `json:"customPorts,omitempty"`
	GPUModel       string           `json:"gpuModel,omitempty"`
	Location       string           `json:"location,omitempty"`
}

// SupportsFeature advertises what a rental's provider can do.
type SupportsFeature struct {
	AttachedStorage bool `json:"attachedStorage"`
	PortForwarding  bool `json:"portForwarding"`
	CustomPorts     bool `json:"customPorts"`
}

// CurrentTerm is the pricing term. costPerHour is in CENTS/hour (verified live:
// 3192 == $31.92/hr), same units as RentalOption.costPerHourCents.
type CurrentTerm struct {
	TermType    string  `json:"termType"`
	CostPerHour float64 `json:"costPerHour"`
}

// Instance is a per-node live view (forge providers populate this).
type Instance struct {
	InstanceRef       string   `json:"instanceRef"`
	Phase             string   `json:"phase"`
	SSH               string   `json:"ssh,omitempty"`
	InternalIP        string   `json:"internalIp,omitempty"`
	AgentVersion      string   `json:"agentVersion,omitempty"`
	SupportedFeatures []string `json:"supportedFeatures,omitempty"`
}

// SupportsGuestReboot reports whether this instance can be rebooted in-guest.
func (i Instance) SupportsGuestReboot() bool {
	for _, f := range i.SupportedFeatures {
		if f == FeatureGuestReboot {
			return true
		}
	}
	return false
}

// RentalRow is the bare row returned by POST create (no status/instances/term).
type RentalRow struct {
	ID           int64        `json:"id"`       // numeric serial id — terminate/reboot use THIS
	RentalID     string       `json:"rentalId"` // string nanoid external id
	ExternalID   string       `json:"externalId"`
	Name         *string      `json:"name"`
	Label        *string      `json:"label"`
	Region       *string      `json:"region"`
	GPUType      string       `json:"gpuType"`
	GPUCount     int          `json:"gpuCount"`
	RentalType   string       `json:"rentalType"`
	ProviderData ProviderData `json:"providerData"`
}

// VMRental is the rich row from GET list (with status/instances/currentTerm).
type VMRental struct {
	ID              int64           `json:"id"`
	RentalID        string          `json:"rentalId"`
	ExternalID      string          `json:"externalId"`
	Name            *string         `json:"name"`
	Label           *string         `json:"label"`
	Region          string          `json:"region"`
	GPUType         string          `json:"gpuType"`
	GPUCount        int             `json:"gpuCount"`
	Status          string          `json:"status"`
	ConnectionType  string          `json:"connectionType,omitempty"`
	EthernetVariant string          `json:"ethernetVariant,omitempty"`
	CurrentTerm     CurrentTerm     `json:"currentTerm"`
	SupportsFeature SupportsFeature `json:"supportsFeature"`
	ProviderData    ProviderData    `json:"providerData"`
	Instances       []Instance      `json:"instances,omitempty"`
}

// CreateVMRentalRequest is the POST body. Zero-valued optional fields are
// omitted so the server applies its own defaults where appropriate.
type CreateVMRentalRequest struct {
	GPUCount    int    `json:"gpuCount"`
	GPUType     string `json:"gpuType"`
	Region      string `json:"region"`
	NetworkType string `json:"networkType,omitempty"`
	Label       string `json:"label,omitempty"`
	TermType    string `json:"termType"`
	CustomPorts []int  `json:"customPorts,omitempty"`
}

// terminateRequest / rebootRequest are internal request bodies.
type terminateRequest struct {
	RentalID int64  `json:"rentalId"` // the numeric id
	Reason   string `json:"reason,omitempty"`
}

type rebootRequest struct {
	RentalID    int64  `json:"rentalId"` // the numeric id
	InstanceRef string `json:"instanceRef"`
}
