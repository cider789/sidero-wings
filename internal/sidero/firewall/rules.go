// Package firewall defines and transactionally applies the structured Sidero
// firewall desired state when the disabled-by-default nftables backend is available.
package firewall

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/netip"
	"strconv"
	"strings"
	"time"
)

var ErrInvalidRule = errors.New("sidero firewall: invalid rule")
var ErrUnsupported = errors.New("sidero firewall: nftables backend unavailable")

type Request struct {
	AllocationID int64      `json:"allocation_id"`
	Protocol     string     `json:"protocol"`
	Source       string     `json:"source"`
	Action       string     `json:"action"`
	ExpiresAt    *time.Time `json:"expires_at,omitempty"`
	Description  string     `json:"description,omitempty"`
}
type Rule struct {
	ID           string     `json:"id"`
	AllocationID int64      `json:"allocation_id"`
	Protocol     string     `json:"protocol"`
	Source       string     `json:"source"`
	Action       string     `json:"action"`
	ExpiresAt    *time.Time `json:"expires_at,omitempty"`
	Description  string     `json:"description,omitempty"`
}

func Validate(serverID string, request Request, now time.Time) (Rule, error) {
	request.Protocol = strings.ToLower(request.Protocol)
	request.Action = strings.ToLower(request.Action)
	if serverID == "" || request.AllocationID <= 0 || request.Protocol != "tcp" && request.Protocol != "udp" || request.Action != "allow" && request.Action != "deny" || len(request.Description) > 200 {
		return Rule{}, ErrInvalidRule
	}
	prefix, err := netip.ParsePrefix(request.Source)
	if err != nil {
		address, addrErr := netip.ParseAddr(request.Source)
		if addrErr != nil {
			return Rule{}, ErrInvalidRule
		}
		bits := 128
		if address.Is4() {
			bits = 32
		}
		prefix = netip.PrefixFrom(address, bits)
	}
	prefix = prefix.Masked()
	if !prefix.Addr().IsValid() || prefix.Addr().IsUnspecified() || prefix.Addr().IsMulticast() {
		return Rule{}, ErrInvalidRule
	}
	if request.ExpiresAt != nil && (!request.ExpiresAt.After(now) || request.ExpiresAt.After(now.Add(30*24*time.Hour))) {
		return Rule{}, ErrInvalidRule
	}
	identity := serverID + "\x00" + strconv.FormatInt(request.AllocationID, 10) + "\x00" + request.Protocol + "\x00" + prefix.String() + "\x00" + request.Action
	if request.ExpiresAt != nil {
		identity += "\x00" + request.ExpiresAt.UTC().Format(time.RFC3339Nano)
	}
	sum := sha256.Sum256([]byte(identity))
	return Rule{ID: hex.EncodeToString(sum[:16]), AllocationID: request.AllocationID, Protocol: request.Protocol, Source: prefix.String(), Action: request.Action, ExpiresAt: request.ExpiresAt, Description: request.Description}, nil
}

func SourcePermitted(source string, allowed, blocked []string) bool {
	requested, err := parseSourcePrefix(source)
	if err != nil {
		return false
	}
	for _, value := range blocked {
		candidate, err := netip.ParsePrefix(value)
		if err != nil || candidate.Addr().Is4() != requested.Addr().Is4() {
			continue
		}
		candidate = candidate.Masked()
		if candidate.Contains(requested.Addr()) || requested.Contains(candidate.Addr()) {
			return false
		}
	}
	if len(allowed) == 0 {
		return true
	}
	for _, value := range allowed {
		candidate, err := netip.ParsePrefix(value)
		if err != nil || candidate.Addr().Is4() != requested.Addr().Is4() {
			continue
		}
		candidate = candidate.Masked()
		if candidate.Contains(requested.Addr()) && requested.Bits() >= candidate.Bits() {
			return true
		}
	}
	return false
}

func parseSourcePrefix(source string) (netip.Prefix, error) {
	prefix, err := netip.ParsePrefix(source)
	if err == nil {
		return prefix.Masked(), nil
	}
	address, err := netip.ParseAddr(source)
	if err != nil {
		return netip.Prefix{}, err
	}
	bits := 128
	if address.Is4() {
		bits = 32
	}
	return netip.PrefixFrom(address, bits), nil
}
