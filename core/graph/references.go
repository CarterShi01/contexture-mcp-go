package graph

import (
	"encoding/base64"
	"strings"
)

const MaxReferenceBytes = 1024

// ManagedReference is immutable syntax identity, not authority or freshness.
// Its zero value is invalid; constructors and Wire validate it explicitly.
type ManagedReference struct{ graphID, generationUUID, nodeID string }

func (r ManagedReference) GraphID() string        { return r.graphID }
func (r ManagedReference) GenerationUUID() string { return r.generationUUID }
func (r ManagedReference) NodeID() string         { return r.nodeID }
func (r ManagedReference) Wire() (string, error) {
	return FormatReference(r.graphID, r.generationUUID, r.nodeID)
}

func NewManagedReference(graphID, generationUUID, nodeID string) (ManagedReference, error) {
	if _, err := ValidateID(graphID); err != nil {
		return ManagedReference{}, err
	}
	if _, err := ValidateID(nodeID); err != nil {
		return ManagedReference{}, err
	}
	if !validGeneration(generationUUID) {
		return ManagedReference{}, invalid("Invalid canonical managed reference")
	}
	return ManagedReference{graphID, generationUUID, nodeID}, nil
}

func validGeneration(value string) bool {
	if len(value) != 36 {
		return false
	}
	for i := range len(value) {
		if i == 8 || i == 13 || i == 18 || i == 23 {
			if value[i] != '-' {
				return false
			}
			continue
		}
		if !(value[i] >= '0' && value[i] <= '9' || value[i] >= 'a' && value[i] <= 'f') {
			return false
		}
	}
	return true
}

func FormatReference(graphID, generationUUID, nodeID string) (string, error) {
	r, err := NewManagedReference(graphID, generationUUID, nodeID)
	if err != nil {
		return "", err
	}
	wire := "ctxg:v1:" + base64.RawURLEncoding.EncodeToString([]byte(r.graphID)) + ":" + r.generationUUID + ":" + base64.RawURLEncoding.EncodeToString([]byte(r.nodeID))
	if len(wire) > MaxReferenceBytes {
		return "", invalid("Invalid canonical managed reference")
	}
	return wire, nil
}

func decodeID(value string) (string, error) {
	if len(value) < 1 || len(value) > 342 {
		return "", invalid("Invalid canonical managed reference")
	}
	for _, c := range value {
		if !(c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '_' || c == '-') {
			return "", invalid("Invalid canonical managed reference")
		}
	}
	data, err := base64.RawURLEncoding.Strict().DecodeString(value)
	if err != nil || base64.RawURLEncoding.EncodeToString(data) != value {
		return "", invalid("Invalid canonical managed reference")
	}
	return ValidateID(string(data))
}

func ParseReference(value string) (ManagedReference, error) {
	if len(value) > MaxReferenceBytes {
		return ManagedReference{}, invalid("Invalid canonical managed reference")
	}
	for _, c := range value {
		if c > 127 {
			return ManagedReference{}, invalid("Invalid canonical managed reference")
		}
	}
	parts := strings.Split(value, ":")
	if len(parts) != 5 || parts[0] != "ctxg" || parts[1] != "v1" {
		return ManagedReference{}, invalid("Invalid canonical managed reference")
	}
	graphID, err := decodeID(parts[2])
	if err != nil {
		return ManagedReference{}, err
	}
	nodeID, err := decodeID(parts[4])
	if err != nil {
		return ManagedReference{}, err
	}
	r, err := NewManagedReference(graphID, parts[3], nodeID)
	if err != nil {
		return ManagedReference{}, err
	}
	wire, err := r.Wire()
	if err != nil || wire != value {
		return ManagedReference{}, invalid("Invalid canonical managed reference")
	}
	return r, nil
}
