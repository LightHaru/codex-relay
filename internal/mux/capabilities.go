package mux

import (
	"fmt"
	"strings"
	"time"

	"github.com/LightHaru/codex-relay/internal/state"
)

func (m *Multiplexer) effectiveRoutingPolicy() state.RoutingPolicy {
	if !m.safeHandoff {
		return state.RoutingPolicySticky
	}
	return m.store.RoutingPolicy()
}

func (m *Multiplexer) safeHandoffUnavailableError() error {
	profile := strings.TrimSpace(m.compatibilityProfile)
	if profile == "" {
		profile = "unknown"
	}
	return fmt.Errorf("safe cross-account handoff is unavailable for app-server profile %q; this task remains Sticky on its current account", profile)
}

func (m *Multiplexer) recordThreadReadCapability(accountID string) {
	capability, _ := m.store.AccountCapability(accountID)
	capability.AccountID = accountID
	capability.Profile = compatibilityProfileOrUnknown(m.compatibilityProfile)
	capability.Known = m.safeHandoff
	capability.ThreadRead = true
	capability.ObservedAt = time.Now().UnixMilli()
	_ = m.store.PutAccountCapability(capability)
}

func (m *Multiplexer) recordResumeCapability(accountID string) {
	capability, _ := m.store.AccountCapability(accountID)
	capability.AccountID = accountID
	capability.Profile = compatibilityProfileOrUnknown(m.compatibilityProfile)
	capability.Known = m.safeHandoff
	capability.ResumeByID = true
	// No supported app-server profile currently proves that an incomplete turn
	// can be resumed without replaying side effects. Keep this false until a
	// future adapter supplies an explicit capability.
	capability.IncompleteTurnResume = false
	capability.ObservedAt = time.Now().UnixMilli()
	_ = m.store.PutAccountCapability(capability)
}

func unknownCapability(accountID string) state.AccountCapability {
	return state.AccountCapability{AccountID: accountID, Profile: "unknown", Known: false}
}

func (m *Multiplexer) baseCapability(accountID string) state.AccountCapability {
	if !m.safeHandoff {
		return unknownCapability(accountID)
	}
	return state.AccountCapability{
		AccountID: accountID, Profile: compatibilityProfileOrUnknown(m.compatibilityProfile),
		Known: true, ThreadRead: true, ResumeByID: true, IncompleteTurnResume: false,
	}
}
