// Package model defines the shared domain types used across the application.
package model

import "time"

// RecordType represents a DNS record type supported by dns.he.net.
// All 17 types that HE.net supports are defined here.
type RecordType string

const (
	// Standard record types
	RecordTypeA     RecordType = "A"
	RecordTypeAAAA  RecordType = "AAAA"
	RecordTypeCNAME RecordType = "CNAME"
	RecordTypeALIAS RecordType = "ALIAS"
	RecordTypeMX    RecordType = "MX"
	RecordTypeNS    RecordType = "NS"
	RecordTypeTXT   RecordType = "TXT"

	// Additional supported types
	RecordTypeCAA   RecordType = "CAA"
	RecordTypeAFSDB RecordType = "AFSDB"
	RecordTypeHINFO RecordType = "HINFO"
	RecordTypeRP    RecordType = "RP"
	RecordTypeLOC   RecordType = "LOC"
	RecordTypeNAPTR RecordType = "NAPTR"
	RecordTypePTR   RecordType = "PTR"
	RecordTypeSSHFP RecordType = "SSHFP"
	RecordTypeSPF   RecordType = "SPF"
	RecordTypeSRV   RecordType = "SRV"
)

// Account represents a dns.he.net account with its HE credentials.
//
// SECURITY (SEC-03):
//   Password is stored in SQLite (0600 permissions) as of migration 005.
//   It is tagged json:"-" so it is NEVER serialized into REST API responses.
//   The REST API returns only id, username, created_at — password stays server-side.
//   For higher-security deployments use the Vault credential provider instead.
type Account struct {
	ID        string    `json:"id"`
	Username  string    `json:"username"`
	Password  string    `json:"-"` // Never in API responses; used by DBProvider and admin UI
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// Zone represents a DNS zone managed in dns.he.net.
type Zone struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	AccountID string `json:"account_id"`
}

// Record represents a DNS record within a zone.
// Field usage varies by record type (e.g., Priority is only for MX/SRV,
// Weight/Port/Target are only for SRV).
type Record struct {
	ID       string     `json:"id"`
	ZoneID   string     `json:"zone_id"`
	Type     RecordType `json:"type"`
	Name     string     `json:"name"`
	Content  string     `json:"content"`
	TTL      int        `json:"ttl"`
	Priority int        `json:"priority"` // MX, SRV
	Weight   int        `json:"weight"`   // SRV only
	Port     int        `json:"port"`     // SRV only
	Target   string     `json:"target"`   // SRV only
	Dynamic  bool       `json:"dynamic"`  // DDNS checkbox (A, AAAA, TXT, AFSDB)
}
