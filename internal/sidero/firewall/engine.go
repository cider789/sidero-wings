package firewall

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

var safeRuleID = regexp.MustCompile(`^[a-f0-9]{1,64}$`)

var ErrApplyFailed = errors.New("sidero firewall: apply failed")
var ErrRuleLimit = errors.New("sidero firewall: rule limit reached")
var ErrRuleNotFound = errors.New("sidero firewall: rule not found")
var ErrRuleConflict = errors.New("sidero firewall: rule conflict")

type AppliedRule struct {
	Rule
	ServerID    string `json:"-"`
	Destination string `json:"-"`
	Port        int    `json:"port"`
}

type Backend interface {
	Available() bool
	DryRun(context.Context, []AppliedRule) error
	Apply(context.Context, []AppliedRule) error
}

type Store interface {
	Save(context.Context, []AppliedRule) error
}

type AuditFunc func(serverID, action, ruleID string)

type Engine struct {
	backend Backend
	maximum int
	audit   AuditFunc
	store   Store
	mu      sync.RWMutex
	rules   map[string]map[string]AppliedRule
}

func NewEngine(backend Backend, maximum int, audit AuditFunc) *Engine {
	if maximum < 1 {
		maximum = 1
	}
	return &Engine{backend: backend, maximum: maximum, audit: audit, rules: map[string]map[string]AppliedRule{}}
}

func (e *Engine) Available() bool { return e.backend != nil && e.backend.Available() }

func (e *Engine) SetStore(store Store) { e.store = store }

func (e *Engine) DryRun(ctx context.Context, serverID, destination string, port int, request Request, now time.Time) (AppliedRule, error) {
	if !e.Available() {
		return AppliedRule{}, ErrUnsupported
	}
	applied, err := prepareAppliedRule(serverID, destination, port, request, now)
	if err != nil {
		return AppliedRule{}, ErrInvalidRule
	}
	e.mu.RLock()
	desired := e.snapshotLocked()
	e.mu.RUnlock()
	desired = appendOrReplace(desired, applied)
	if err := e.backend.DryRun(ctx, desired); err != nil {
		return AppliedRule{}, fmt.Errorf("%w: %v", ErrApplyFailed, err)
	}
	return applied, nil
}

func (e *Engine) Add(ctx context.Context, serverID, destination string, port int, request Request, now time.Time) (AppliedRule, error) {
	if !e.Available() {
		return AppliedRule{}, ErrUnsupported
	}
	applied, err := prepareAppliedRule(serverID, destination, port, request, now)
	if err != nil {
		return AppliedRule{}, ErrInvalidRule
	}
	e.mu.Lock()
	serverRules := e.rules[serverID]
	if serverRules == nil {
		serverRules = map[string]AppliedRule{}
		e.rules[serverID] = serverRules
	}
	existing, existed := serverRules[applied.ID]
	if existed {
		if sameApplied(existing, applied) {
			e.mu.Unlock()
			return existing, nil
		}
	}
	for _, existing := range serverRules {
		if existing.AllocationID == applied.AllocationID && existing.Protocol == applied.Protocol && existing.Source == applied.Source && existing.Action != applied.Action {
			e.mu.Unlock()
			return AppliedRule{}, ErrRuleConflict
		}
	}
	if !existed && len(serverRules) >= e.maximum {
		e.mu.Unlock()
		return AppliedRule{}, ErrRuleLimit
	}
	previous := e.snapshotLocked()
	serverRules[applied.ID] = applied
	desired := e.snapshotLocked()
	if err := e.backend.Apply(ctx, desired); err != nil {
		e.rules = rulesFromSnapshot(previous)
		rollbackErr := e.backend.Apply(ctx, previous)
		e.mu.Unlock()
		if rollbackErr != nil {
			return AppliedRule{}, errors.Join(fmt.Errorf("%w: %v", ErrApplyFailed, err), rollbackErr)
		}
		return AppliedRule{}, fmt.Errorf("%w: %v", ErrApplyFailed, err)
	}
	if e.store != nil {
		if err := e.store.Save(ctx, desired); err != nil {
			e.rules = rulesFromSnapshot(previous)
			rollbackErr := e.backend.Apply(ctx, previous)
			e.mu.Unlock()
			return AppliedRule{}, errors.Join(fmt.Errorf("%w: state persistence failed", ErrApplyFailed), rollbackErr)
		}
	}
	e.mu.Unlock()
	if e.audit != nil {
		e.audit(serverID, "created", applied.ID)
	}
	return applied, nil
}

func (e *Engine) Remove(ctx context.Context, serverID, ruleID string) error {
	if !e.Available() {
		return ErrUnsupported
	}
	e.mu.Lock()
	serverRules := e.rules[serverID]
	rule, ok := serverRules[ruleID]
	if !ok {
		e.mu.Unlock()
		return ErrRuleNotFound
	}
	previous := e.snapshotLocked()
	delete(serverRules, ruleID)
	desired := e.snapshotLocked()
	if err := e.backend.Apply(ctx, desired); err != nil {
		serverRules[ruleID] = rule
		rollbackErr := e.backend.Apply(ctx, previous)
		e.mu.Unlock()
		if rollbackErr != nil {
			return errors.Join(fmt.Errorf("%w: %v", ErrApplyFailed, err), rollbackErr)
		}
		return fmt.Errorf("%w: %v", ErrApplyFailed, err)
	}
	if e.store != nil {
		if err := e.store.Save(ctx, desired); err != nil {
			e.rules = rulesFromSnapshot(previous)
			rollbackErr := e.backend.Apply(ctx, previous)
			e.mu.Unlock()
			return errors.Join(fmt.Errorf("%w: state persistence failed", ErrApplyFailed), rollbackErr)
		}
	}
	e.mu.Unlock()
	if e.audit != nil {
		e.audit(serverID, "removed", ruleID)
	}
	return nil
}

func (e *Engine) RemoveServer(ctx context.Context, serverID string) error {
	if !e.Available() {
		return ErrUnsupported
	}
	e.mu.Lock()
	if len(e.rules[serverID]) == 0 {
		e.mu.Unlock()
		return nil
	}
	previous := e.snapshotLocked()
	delete(e.rules, serverID)
	desired := e.snapshotLocked()
	if err := e.backend.Apply(ctx, desired); err != nil {
		e.rules = rulesFromSnapshot(previous)
		rollbackErr := e.backend.Apply(ctx, previous)
		e.mu.Unlock()
		return errors.Join(fmt.Errorf("%w: %v", ErrApplyFailed, err), rollbackErr)
	}
	if e.store != nil {
		if err := e.store.Save(ctx, desired); err != nil {
			e.rules = rulesFromSnapshot(previous)
			rollbackErr := e.backend.Apply(ctx, previous)
			e.mu.Unlock()
			return errors.Join(fmt.Errorf("%w: state persistence failed", ErrApplyFailed), rollbackErr)
		}
	}
	e.mu.Unlock()
	for _, rule := range previous {
		if rule.ServerID == serverID && e.audit != nil {
			e.audit(serverID, "removed", rule.ID)
		}
	}
	return nil
}

func (e *Engine) List(serverID string) []AppliedRule {
	e.mu.RLock()
	defer e.mu.RUnlock()
	out := make([]AppliedRule, 0, len(e.rules[serverID]))
	for _, rule := range e.rules[serverID] {
		out = append(out, rule)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func (e *Engine) Reconcile(ctx context.Context) error {
	if !e.Available() {
		return ErrUnsupported
	}
	e.mu.RLock()
	desired := e.snapshotLocked()
	e.mu.RUnlock()
	if err := e.backend.Apply(ctx, desired); err != nil {
		return fmt.Errorf("%w: %v", ErrApplyFailed, err)
	}
	return nil
}

func (e *Engine) CleanupExpired(ctx context.Context, now time.Time, maximum int) int {
	if maximum < 1 || !e.Available() {
		return 0
	}
	e.mu.Lock()
	previous := e.snapshotLocked()
	removed := make([]AppliedRule, 0)
	for serverID, serverRules := range e.rules {
		for id, rule := range serverRules {
			if len(removed) >= maximum {
				break
			}
			if rule.ExpiresAt != nil && !rule.ExpiresAt.After(now) {
				removed = append(removed, rule)
				delete(serverRules, id)
			}
		}
		if len(serverRules) == 0 {
			delete(e.rules, serverID)
		}
		if len(removed) >= maximum {
			break
		}
	}
	if len(removed) == 0 {
		e.mu.Unlock()
		return 0
	}
	desired := e.snapshotLocked()
	if err := e.backend.Apply(ctx, desired); err != nil {
		e.rules = rulesFromSnapshot(previous)
		_ = e.backend.Apply(ctx, previous)
		e.mu.Unlock()
		return 0
	}
	if e.store != nil {
		if err := e.store.Save(ctx, desired); err != nil {
			e.rules = rulesFromSnapshot(previous)
			_ = e.backend.Apply(ctx, previous)
			e.mu.Unlock()
			return 0
		}
	}
	e.mu.Unlock()
	for _, rule := range removed {
		if e.audit != nil {
			e.audit(rule.ServerID, "expired", rule.ID)
		}
	}
	return len(removed)
}

func (e *Engine) snapshotLocked() []AppliedRule {
	out := make([]AppliedRule, 0)
	for _, serverRules := range e.rules {
		for _, rule := range serverRules {
			out = append(out, rule)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].ServerID == out[j].ServerID {
			return out[i].ID < out[j].ID
		}
		return out[i].ServerID < out[j].ServerID
	})
	return out
}

func (e *Engine) Snapshot() []AppliedRule {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.snapshotLocked()
}

func (e *Engine) Restore(rules []AppliedRule) error {
	if _, err := RenderRuleset(rules, false); err != nil {
		return err
	}
	counts := map[string]int{}
	for _, rule := range rules {
		if rule.ServerID == "" {
			return ErrInvalidRule
		}
		counts[rule.ServerID]++
		if counts[rule.ServerID] > e.maximum {
			return ErrRuleLimit
		}
	}
	e.mu.Lock()
	e.rules = rulesFromSnapshot(rules)
	e.mu.Unlock()
	return nil
}

func rulesFromSnapshot(snapshot []AppliedRule) map[string]map[string]AppliedRule {
	out := map[string]map[string]AppliedRule{}
	for _, rule := range snapshot {
		if out[rule.ServerID] == nil {
			out[rule.ServerID] = map[string]AppliedRule{}
		}
		out[rule.ServerID][rule.ID] = rule
	}
	return out
}
func appendOrReplace(rules []AppliedRule, candidate AppliedRule) []AppliedRule {
	for i := range rules {
		if rules[i].ServerID == candidate.ServerID && rules[i].ID == candidate.ID {
			rules[i] = candidate
			return rules
		}
	}
	return append(rules, candidate)
}

func RenderRuleset(rules []AppliedRule, tableExists bool) (string, error) {
	rules = append([]AppliedRule(nil), rules...)
	sort.Slice(rules, func(i, j int) bool {
		if rules[i].AllocationID != rules[j].AllocationID {
			return rules[i].AllocationID < rules[j].AllocationID
		}
		if rules[i].Protocol != rules[j].Protocol {
			return rules[i].Protocol < rules[j].Protocol
		}
		if rules[i].Action != rules[j].Action {
			return rules[i].Action == "deny"
		}
		return rules[i].ID < rules[j].ID
	})
	var builder strings.Builder
	if tableExists {
		builder.WriteString("delete table inet sidero_wings\n")
	}
	builder.WriteString("table inet sidero_wings {\n  chain ingress {\n    type filter hook prerouting priority -150; policy accept;\n")
	allowFallbacks := map[string]string{}
	for _, rule := range rules {
		prefix, err := netip.ParsePrefix(rule.Source)
		destination, destinationErr := netip.ParseAddr(rule.Destination)
		if err != nil || destinationErr != nil || destination.Zone() != "" || prefix.Addr().Is4() != destination.Is4() || !safeRuleID.MatchString(rule.ID) || rule.AllocationID <= 0 || rule.Port < 1 || rule.Port > 65535 || rule.Protocol != "tcp" && rule.Protocol != "udp" || rule.Action != "allow" && rule.Action != "deny" {
			return "", ErrInvalidRule
		}
		family := "ip"
		if prefix.Addr().Is6() {
			family = "ip6"
		}
		destinationMatch := ""
		if !destination.IsUnspecified() {
			destinationMatch = fmt.Sprintf("%s daddr %s ", family, destination)
		}
		verdict := "accept"
		if rule.Action == "deny" {
			verdict = "drop"
		}
		builder.WriteString(fmt.Sprintf("    %s%s saddr %s %s dport %d %s comment \"sidero-%s\"\n", destinationMatch, family, prefix.Masked(), rule.Protocol, rule.Port, verdict, rule.ID))
		if rule.Action == "allow" {
			key := fmt.Sprintf("%d/%s/%s/%d", rule.AllocationID, rule.Protocol, destination, rule.Port)
			allowFallbacks[key] = fmt.Sprintf("    %s%s dport %d drop\n", destinationMatch, rule.Protocol, rule.Port)
		}
	}
	keys := make([]string, 0, len(allowFallbacks))
	for key := range allowFallbacks {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		builder.WriteString(allowFallbacks[key])
	}
	builder.WriteString("  }\n}\n")
	return builder.String(), nil
}

func prepareAppliedRule(serverID, destination string, port int, request Request, now time.Time) (AppliedRule, error) {
	rule, err := Validate(serverID, request, now)
	address, addressErr := netip.ParseAddr(destination)
	prefix, prefixErr := netip.ParsePrefix(rule.Source)
	if err != nil || addressErr != nil || prefixErr != nil || address.Zone() != "" || address.IsMulticast() || prefix.Addr().Is4() != address.Is4() || port < 1 || port > 65535 {
		return AppliedRule{}, ErrInvalidRule
	}
	return AppliedRule{Rule: rule, ServerID: serverID, Destination: address.String(), Port: port}, nil
}

func sameApplied(left, right AppliedRule) bool {
	return left.ID == right.ID && left.ServerID == right.ServerID && left.Destination == right.Destination && left.Port == right.Port
}
