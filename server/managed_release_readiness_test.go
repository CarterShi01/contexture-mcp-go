package server

import "testing"

func TestDurableReleaseBenignJournalContentionDoesNotRequireRecovery(t *testing.T) {
	f := newReleaseFixture(t, nil)
	owner := f.owner()
	if _, err := owner.Request(f.request(nil)); err != nil {
		t.Fatal(err)
	}
	f.journal.mu.Lock()
	_, err := owner.Capture()
	f.journal.mu.Unlock()
	if err == nil || owner.SwapsFrozen() {
		t.Fatal("healthy Journal contention poisoned release custody", err)
	}
	if captured, err := owner.Capture(); err != nil || captured == nil {
		t.Fatal("healthy custody required reopen after contention", captured, err)
	}
}
