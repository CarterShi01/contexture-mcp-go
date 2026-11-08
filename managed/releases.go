package managed

// IsUnclaimedGeneration is a local custody check for resource-provider adapters.
// It does not grant release or request authority.
func IsUnclaimedGeneration(generation *ReadyGeneration) bool {
	if generation == nil {
		return false
	}
	generation.mu.Lock()
	defer generation.mu.Unlock()
	return generation.owner == nil && generation.state == "prepared"
}
