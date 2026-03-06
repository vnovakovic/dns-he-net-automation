package validate_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/vnovakovic/dns-he-net-automation/internal/api/validate"
	"github.com/vnovakovic/dns-he-net-automation/internal/model"
)

// baseRecord returns a minimally valid model.Record for the given record type.
// Caller can override individual fields to test specific failure modes.
func baseRecord(t model.RecordType) model.Record {
	rec := model.Record{
		Type: t,
		Name: "test.example.com",
		TTL:  300,
	}
	switch t {
	case model.RecordTypeA:
		rec.Content = "1.2.3.4"
	case model.RecordTypeAAAA:
		rec.Content = "2001:db8::1"
	case model.RecordTypeCNAME:
		rec.Content = "target.example.com"
	case model.RecordTypeNS:
		rec.Content = "ns1.example.com"
	case model.RecordTypeMX:
		rec.Content = "mail.example.com"
		rec.Priority = 10
	case model.RecordTypeTXT:
		rec.Content = "v=spf1 include:example.com ~all"
	case model.RecordTypeSRV:
		rec.Priority = 10
		rec.Weight = 5
		rec.Port = 443
		rec.Target = "svc.example.com"
	case model.RecordTypeCAA:
		rec.Content = "0 issue letsencrypt.org"
	}
	return rec
}

func TestValidateRecord(t *testing.T) {
	tests := []struct {
		name    string
		rec     model.Record
		wantErr bool
		errMsg  string // substring expected in error message when wantErr is true
	}{
		// --- Type validation ---
		{
			name:    "unsupported type NAPTR",
			rec:     model.Record{Type: "NAPTR", Name: "test.example.com", TTL: 300},
			wantErr: true,
			errMsg:  "unsupported",
		},
		{
			name:    "unsupported type empty string",
			rec:     model.Record{Type: "", Name: "test.example.com", TTL: 300},
			wantErr: true,
			errMsg:  "unsupported",
		},
		{
			name:    "unsupported type ALIAS (not in v1 set)",
			rec:     model.Record{Type: "ALIAS", Name: "test.example.com", TTL: 300},
			wantErr: true,
			errMsg:  "unsupported",
		},

		// --- Name validation ---
		{
			name:    "empty name",
			rec:     func() model.Record { r := baseRecord(model.RecordTypeA); r.Name = ""; return r }(),
			wantErr: true,
			errMsg:  "name is required",
		},
		{
			name:    "whitespace-only name",
			rec:     func() model.Record { r := baseRecord(model.RecordTypeA); r.Name = "   "; return r }(),
			wantErr: true,
			errMsg:  "name is required",
		},
		{
			name:    "name exactly 254 chars",
			rec:     func() model.Record { r := baseRecord(model.RecordTypeA); r.Name = strings.Repeat("a", 254); return r }(),
			wantErr: true,
			errMsg:  "253",
		},
		{
			name:    "name exactly 253 chars (boundary, valid)",
			rec:     func() model.Record { r := baseRecord(model.RecordTypeA); r.Name = strings.Repeat("a", 253); return r }(),
			wantErr: false,
		},

		// --- TTL validation ---
		{
			name:    "TTL 0 (invalid)",
			rec:     func() model.Record { r := baseRecord(model.RecordTypeA); r.TTL = 0; return r }(),
			wantErr: true,
			errMsg:  "invalid TTL",
		},
		{
			name:    "TTL 600 (not in allowed set)",
			rec:     func() model.Record { r := baseRecord(model.RecordTypeA); r.TTL = 600; return r }(),
			wantErr: true,
			errMsg:  "invalid TTL",
		},
		{
			name:    "TTL 300 (minimum allowed)",
			rec:     func() model.Record { r := baseRecord(model.RecordTypeA); r.TTL = 300; return r }(),
			wantErr: false,
		},
		{
			name:    "TTL 172800 (maximum allowed)",
			rec:     func() model.Record { r := baseRecord(model.RecordTypeA); r.TTL = 172800; return r }(),
			wantErr: false,
		},
		{
			name:    "TTL 3600 (common value, valid)",
			rec:     func() model.Record { r := baseRecord(model.RecordTypeA); r.TTL = 3600; return r }(),
			wantErr: false,
		},

		// --- A record ---
		{
			name:    "A valid IPv4 1.2.3.4",
			rec:     baseRecord(model.RecordTypeA),
			wantErr: false,
		},
		{
			name:    "A invalid content not-an-ip",
			rec:     func() model.Record { r := baseRecord(model.RecordTypeA); r.Content = "not-an-ip"; return r }(),
			wantErr: true,
			errMsg:  "invalid IPv4",
		},
		{
			name:    "A with IPv6 address (::1 is not IPv4)",
			rec:     func() model.Record { r := baseRecord(model.RecordTypeA); r.Content = "::1"; return r }(),
			wantErr: true,
			errMsg:  "invalid IPv4",
		},
		{
			name:    "A empty content",
			rec:     func() model.Record { r := baseRecord(model.RecordTypeA); r.Content = ""; return r }(),
			wantErr: true,
			errMsg:  "invalid IPv4",
		},

		// --- AAAA record ---
		{
			name:    "AAAA valid IPv6 2001:db8::1",
			rec:     baseRecord(model.RecordTypeAAAA),
			wantErr: false,
		},
		{
			name:    "AAAA with IPv4 address",
			rec:     func() model.Record { r := baseRecord(model.RecordTypeAAAA); r.Content = "1.2.3.4"; return r }(),
			wantErr: true,
			errMsg:  "IPv4",
		},
		{
			name:    "AAAA invalid content not-an-ip",
			rec:     func() model.Record { r := baseRecord(model.RecordTypeAAAA); r.Content = "not-an-ip"; return r }(),
			wantErr: true,
			errMsg:  "invalid IPv6",
		},

		// --- MX record ---
		{
			name:    "MX valid",
			rec:     baseRecord(model.RecordTypeMX),
			wantErr: false,
		},
		{
			name:    "MX priority 0 (invalid, must be >= 1)",
			rec:     func() model.Record { r := baseRecord(model.RecordTypeMX); r.Priority = 0; return r }(),
			wantErr: true,
			errMsg:  "1-65535",
		},
		{
			name:    "MX priority 65536 (out of range)",
			rec:     func() model.Record { r := baseRecord(model.RecordTypeMX); r.Priority = 65536; return r }(),
			wantErr: true,
			errMsg:  "1-65535",
		},
		{
			name:    "MX empty content",
			rec:     func() model.Record { r := baseRecord(model.RecordTypeMX); r.Content = ""; return r }(),
			wantErr: true,
			errMsg:  "required",
		},
		{
			name:    "MX priority 65535 (boundary, valid)",
			rec:     func() model.Record { r := baseRecord(model.RecordTypeMX); r.Priority = 65535; return r }(),
			wantErr: false,
		},

		// --- SRV record ---
		{
			name:    "SRV valid",
			rec:     baseRecord(model.RecordTypeSRV),
			wantErr: false,
		},
		{
			name:    "SRV port 0 (invalid, must be >= 1)",
			rec:     func() model.Record { r := baseRecord(model.RecordTypeSRV); r.Port = 0; return r }(),
			wantErr: true,
			errMsg:  "1-65535",
		},
		{
			name:    "SRV weight -1 (invalid)",
			rec:     func() model.Record { r := baseRecord(model.RecordTypeSRV); r.Weight = -1; return r }(),
			wantErr: true,
			errMsg:  "0-65535",
		},
		{
			name:    "SRV weight 0 (valid, 0 is allowed)",
			rec:     func() model.Record { r := baseRecord(model.RecordTypeSRV); r.Weight = 0; return r }(),
			wantErr: false,
		},
		{
			name:    "SRV empty target",
			rec:     func() model.Record { r := baseRecord(model.RecordTypeSRV); r.Target = ""; return r }(),
			wantErr: true,
			errMsg:  "required",
		},
		{
			name:    "SRV priority 0 (invalid)",
			rec:     func() model.Record { r := baseRecord(model.RecordTypeSRV); r.Priority = 0; return r }(),
			wantErr: true,
			errMsg:  "1-65535",
		},

		// --- CAA record ---
		{
			name:    "CAA valid 0 issue letsencrypt.org",
			rec:     baseRecord(model.RecordTypeCAA),
			wantErr: false,
		},
		{
			name:    "CAA only 2 tokens (0 issue)",
			rec:     func() model.Record { r := baseRecord(model.RecordTypeCAA); r.Content = "0 issue"; return r }(),
			wantErr: true,
			errMsg:  "flags tag value",
		},
		{
			name:    "CAA empty content",
			rec:     func() model.Record { r := baseRecord(model.RecordTypeCAA); r.Content = ""; return r }(),
			wantErr: true,
			errMsg:  "required",
		},
		{
			name:    "CAA valid issuewild tag",
			rec:     func() model.Record { r := baseRecord(model.RecordTypeCAA); r.Content = "0 issuewild letsencrypt.org"; return r }(),
			wantErr: false,
		},

		// --- CNAME record ---
		{
			name:    "CNAME valid",
			rec:     baseRecord(model.RecordTypeCNAME),
			wantErr: false,
		},
		{
			name:    "CNAME empty content",
			rec:     func() model.Record { r := baseRecord(model.RecordTypeCNAME); r.Content = ""; return r }(),
			wantErr: true,
			errMsg:  "required",
		},

		// --- NS record ---
		{
			name:    "NS valid",
			rec:     baseRecord(model.RecordTypeNS),
			wantErr: false,
		},
		{
			name:    "NS empty content",
			rec:     func() model.Record { r := baseRecord(model.RecordTypeNS); r.Content = ""; return r }(),
			wantErr: true,
			errMsg:  "required",
		},

		// --- TXT record ---
		{
			name:    "TXT valid",
			rec:     baseRecord(model.RecordTypeTXT),
			wantErr: false,
		},
		{
			name:    "TXT empty content",
			rec:     func() model.Record { r := baseRecord(model.RecordTypeTXT); r.Content = ""; return r }(),
			wantErr: true,
			errMsg:  "required",
		},
		{
			name:    "TXT whitespace-only content",
			rec:     func() model.Record { r := baseRecord(model.RecordTypeTXT); r.Content = "   "; return r }(),
			wantErr: true,
			errMsg:  "required",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validate.ValidateRecord(tt.rec)
			if tt.wantErr {
				assert.Error(t, err, "expected an error but got nil")
				if tt.errMsg != "" {
					assert.Contains(t, err.Error(), tt.errMsg,
						"error message should contain %q, got: %s", tt.errMsg, err.Error())
				}
			} else {
				assert.NoError(t, err, "expected no error but got: %v", err)
			}
		})
	}
}
