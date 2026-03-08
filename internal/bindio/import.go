package bindio

import (
	"fmt"
	"strings"

	"github.com/miekg/dns"
	"github.com/vnovakovic/dns-he-net-automation/internal/model"
)

// SkippedRecord represents a record that was not imported.
//
// The Reason field distinguishes between "unsupported type" (informational) and
// parse errors. Skipped records go into the response skipped[] array, not had_errors.
//
// WHY skipped is not an error:
//   CONTEXT.md decision: unsupported types are never errors — they are expected.
//   A BIND zone file from a real deployment may contain HTTPS, TLSA, DNAME, or
//   other types that v1 does not support. Failing the entire import would block
//   migration of zones that happen to have these records. Skipping preserves
//   all supported records and gives the caller visibility into what was omitted.
type SkippedRecord struct {
	Name   string `json:"name"`
	Type   string `json:"type"`
	Reason string `json:"reason"`
}

// ParseZoneFile parses a BIND zone file and returns desired model.Record slice plus
// any records that were skipped with reasons.
//
// Parameters:
//   - body:   raw zone file text (Content-Type: text/plain from request body)
//   - origin: zone's domain name (e.g. "example.com")
//
// WHY origin must be FQDN for ZoneParser:
//   dns.NewZoneParser requires origin with trailing dot so relative names in the
//   file (e.g. "www") resolve to "www.example.com." correctly.
//   Non-FQDN origin causes ZoneParser to produce invalid absolute names.
//   (RESEARCH.md Pitfall 8: always use dns.Fqdn(origin) before passing to ZoneParser.)
//
// SOA records are always skipped — HE manages the SOA and it is never exposed via API.
// Records whose type is not in v1ExportTypes are skipped (informational, not errors).
func ParseZoneFile(body string, origin string) ([]model.Record, []SkippedRecord, error) {
	// dns.Fqdn ensures the origin has a trailing dot required by ZoneParser.
	zp := dns.NewZoneParser(strings.NewReader(body), dns.Fqdn(origin), "import")
	zp.SetDefaultTTL(3600)

	var desired []model.Record
	var skipped []SkippedRecord

	for rr, ok := zp.Next(); ok; rr, ok = zp.Next() {
		hdr := rr.Header()

		// SOA is managed by HE — always skip, never attempt to import.
		if hdr.Rrtype == dns.TypeSOA {
			skipped = append(skipped, SkippedRecord{
				Name:   hdr.Name,
				Type:   "SOA",
				Reason: "SOA is managed by HE and cannot be imported",
			})
			continue
		}

		rec, err := rrToRecord(rr, origin)
		if err != nil {
			// Unsupported type — skip with informational reason, do not fail the import.
			// dns.TypeToString converts uint16 type code to human-readable name (e.g. "HTTPS").
			typeName := dns.TypeToString[hdr.Rrtype]
			if typeName == "" {
				typeName = fmt.Sprintf("TYPE%d", hdr.Rrtype)
			}
			skipped = append(skipped, SkippedRecord{
				Name:   hdr.Name,
				Type:   typeName,
				Reason: err.Error(),
			})
			continue
		}

		desired = append(desired, rec)
	}

	if err := zp.Err(); err != nil {
		return nil, nil, fmt.Errorf("zone file parse error: %w", err)
	}

	return desired, skipped, nil
}

// rrToRecord converts a miekg/dns dns.RR to a model.Record.
// Returns error for unsupported types — caller adds to skipped list.
//
// WHY name normalization strips zone origin suffix:
//   ZoneParser returns FQDN names (e.g. "www.example.com.") even for relative entries.
//   HE's API expects relative names (e.g. "www") or the bare zone name for apex records.
//   We strip the trailing dot, then strip ".origin" suffix to get the relative form.
//   Apex records (name == origin itself) are mapped to "@" which HE accepts for zone apex.
func rrToRecord(rr dns.RR, origin string) (model.Record, error) {
	hdr := rr.Header()

	// Normalize name: strip trailing dot, then strip zone origin suffix if present.
	name := strings.TrimSuffix(hdr.Name, ".")
	originNoTrail := strings.TrimSuffix(dns.Fqdn(origin), ".")
	name = strings.TrimSuffix(name, "."+originNoTrail)
	// If name equals the origin itself (apex record), use "@".
	if name == originNoTrail {
		name = "@"
	}

	rec := model.Record{
		TTL: int(hdr.Ttl),
	}

	switch hdr.Rrtype {
	case dns.TypeA:
		r := rr.(*dns.A)
		rec.Type = model.RecordTypeA
		rec.Name = name
		rec.Content = r.A.String()

	case dns.TypeAAAA:
		r := rr.(*dns.AAAA)
		rec.Type = model.RecordTypeAAAA
		rec.Name = name
		rec.Content = r.AAAA.String()

	case dns.TypeCNAME:
		r := rr.(*dns.CNAME)
		rec.Type = model.RecordTypeCNAME
		rec.Name = name
		// Strip trailing dot — HE stores CNAME targets without trailing dot.
		// WHY r.Target (not r.Cname): miekg/dns CNAME struct uses "Target" field name.
		rec.Content = strings.TrimSuffix(r.Target, ".")

	case dns.TypeMX:
		r := rr.(*dns.MX)
		rec.Type = model.RecordTypeMX
		rec.Name = name
		rec.Priority = int(r.Preference)
		// Strip trailing dot — HE stores MX exchange without trailing dot.
		rec.Content = strings.TrimSuffix(r.Mx, ".")

	case dns.TypeTXT:
		r := rr.(*dns.TXT)
		rec.Type = model.RecordTypeTXT
		rec.Name = name
		// WHY join TXT segments:
		//   miekg/dns splits TXT content at 255-char boundaries into []string.
		//   HE stores TXT as a single string value. Joining on "" reconstructs
		//   the original string without separator artifacts.
		rec.Content = strings.Join(r.Txt, "")

	case dns.TypeNS:
		r := rr.(*dns.NS)
		rec.Type = model.RecordTypeNS
		rec.Name = name
		rec.Content = strings.TrimSuffix(r.Ns, ".")

	case dns.TypeSRV:
		r := rr.(*dns.SRV)
		rec.Type = model.RecordTypeSRV
		rec.Name = name
		rec.Priority = int(r.Priority)
		rec.Weight = int(r.Weight)
		rec.Port = int(r.Port)
		rec.Target = strings.TrimSuffix(r.Target, ".")

	case dns.TypeCAA:
		r := rr.(*dns.CAA)
		rec.Type = model.RecordTypeCAA
		rec.Name = name
		// WHY reconstruct "flags tag value" string:
		//   This is how the v1 API stores CAA content (matching export.go recordToRR).
		//   The three-token format is what the browser form expects and what export produces.
		rec.Content = fmt.Sprintf("%d %s %s", r.Flag, r.Tag, r.Value)

	default:
		typeName := dns.TypeToString[hdr.Rrtype]
		if typeName == "" {
			typeName = fmt.Sprintf("TYPE%d", hdr.Rrtype)
		}
		return model.Record{}, fmt.Errorf("type %s is not supported by the v1 API", typeName)
	}

	return rec, nil
}
