// Package validate provides field-level validation for DNS record API requests.
// Validation is applied before any browser operation so that invalid requests
// are rejected with HTTP 422 without consuming a browser session slot.
package validate

import (
	"errors"
	"fmt"
	"net"
	"strings"

	"github.com/vnovakovic/dns-he-net-automation/internal/model"
)

// allowedTTLs is the complete set of TTL values dns.he.net exposes in the
// record form TTL select element. Any other value is rejected with 422.
var allowedTTLs = map[int]bool{
	300: true, 900: true, 1800: true, 3600: true,
	7200: true, 14400: true, 28800: true, 43200: true,
	86400: true, 172800: true,
}

// v1Types is the v1 API subset of DNS record types supported by the service.
// Replicated here to keep the validate package self-contained and independent
// of handler-level maps.
var v1Types = map[model.RecordType]bool{
	model.RecordTypeA:     true,
	model.RecordTypeAAAA:  true,
	model.RecordTypeCNAME: true,
	model.RecordTypeMX:    true,
	model.RecordTypeTXT:   true,
	model.RecordTypeSRV:   true,
	model.RecordTypeCAA:   true,
	model.RecordTypeNS:    true,
}

// ValidateRecord enforces field constraints for a DNS record before any browser
// operation is attempted. Returns a descriptive error on the first failing rule,
// or nil when the record is valid.
//
// Validation order:
//  1. Type must be in the v1 supported set.
//  2. Name must be non-empty and no longer than 253 characters.
//  3. TTL must be one of the values in the dns.he.net select element.
//  4. Type-specific field constraints (IP format, priority ranges, etc.).
func ValidateRecord(rec model.Record) error {
	// 1. Type check.
	if !v1Types[rec.Type] {
		return fmt.Errorf("unsupported record type %q: must be one of A, AAAA, CNAME, MX, TXT, SRV, CAA, NS", rec.Type)
	}

	// 2. Name check.
	if strings.TrimSpace(rec.Name) == "" {
		return errors.New("name is required")
	}
	if len(rec.Name) > 253 {
		return errors.New("name must not exceed 253 characters")
	}

	// 3. TTL check.
	if !allowedTTLs[rec.TTL] {
		return fmt.Errorf("invalid TTL %d: must be one of 300, 900, 1800, 3600, 7200, 14400, 28800, 43200, 86400, 172800", rec.TTL)
	}

	// 4. Type-specific checks.
	switch rec.Type {
	case model.RecordTypeA:
		ip := net.ParseIP(rec.Content)
		if ip == nil || ip.To4() == nil {
			return fmt.Errorf("invalid IPv4 address %q", rec.Content)
		}

	case model.RecordTypeAAAA:
		ip := net.ParseIP(rec.Content)
		if ip == nil {
			return fmt.Errorf("invalid IPv6 address %q", rec.Content)
		}
		if ip.To4() != nil {
			return fmt.Errorf("%q is an IPv4 address, not IPv6", rec.Content)
		}

	case model.RecordTypeCNAME, model.RecordTypeNS:
		if strings.TrimSpace(rec.Content) == "" {
			return errors.New("content is required for CNAME/NS records")
		}

	case model.RecordTypeMX:
		if strings.TrimSpace(rec.Content) == "" {
			return errors.New("MX content (mail server hostname) is required")
		}
		if rec.Priority < 1 || rec.Priority > 65535 {
			return fmt.Errorf("MX priority must be 1-65535, got %d", rec.Priority)
		}

	case model.RecordTypeTXT:
		if strings.TrimSpace(rec.Content) == "" {
			return errors.New("TXT content is required")
		}

	case model.RecordTypeSRV:
		if rec.Priority < 1 || rec.Priority > 65535 {
			return fmt.Errorf("SRV priority must be 1-65535, got %d", rec.Priority)
		}
		if rec.Weight < 0 || rec.Weight > 65535 {
			return fmt.Errorf("SRV weight must be 0-65535, got %d", rec.Weight)
		}
		if rec.Port < 1 || rec.Port > 65535 {
			return fmt.Errorf("SRV port must be 1-65535, got %d", rec.Port)
		}
		if strings.TrimSpace(rec.Target) == "" {
			return errors.New("SRV target is required")
		}

	case model.RecordTypeCAA:
		if strings.TrimSpace(rec.Content) == "" {
			return errors.New("CAA content is required")
		}
		parts := strings.Fields(strings.TrimSpace(rec.Content))
		if len(parts) < 3 {
			return errors.New("CAA content must be in format: flags tag value")
		}
	}

	return nil
}
