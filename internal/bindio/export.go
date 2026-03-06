// Package bindio provides BIND zone file export and import conversion logic.
//
// Package name is "bindio" (not "sync") to avoid collision with the Go stdlib
// sync package. The io suffix signals bidirectional conversion between model.Record
// and BIND zone file text format.
package bindio

import (
	"fmt"
	"net"
	"strconv"
	"strings"

	"github.com/miekg/dns"
	"github.com/vnovakovic/dns-he-net-automation/internal/model"
)

// ExportZone converts a slice of model.Record to a BIND zone file string.
//
// Export rules (CONTEXT.md decisions):
//   - Only v1ExportTypes are exported (A, AAAA, CNAME, MX, TXT, SRV, CAA, NS).
//   - SOA is excluded — HE manages it and it is never returned by ListRecords.
//   - NS records whose content matches *.he.net are excluded — these are HE's own
//     authoritative nameservers, not user-created delegations (RESEARCH.md Pitfall 7).
//     Filter pattern: content ends in ".he.net" after FQDN normalization.
//     Using suffix match avoids hardcoding specific NS hostnames (ns1.he.net through ns5.he.net).
//   - $ORIGIN is derived from zoneName (FQDN with trailing dot, via dns.Fqdn()).
//   - $TTL is derived from the first record's TTL, or 3600 if records is empty.
//
// Returns the zone file as a string (caller writes to http.ResponseWriter).
func ExportZone(records []model.Record, zoneName string) (string, error) {
	var buf strings.Builder

	defaultTTL := 3600
	if len(records) > 0 {
		defaultTTL = records[0].TTL
	}

	fmt.Fprintf(&buf, "$ORIGIN %s\n", dns.Fqdn(zoneName))
	fmt.Fprintf(&buf, "$TTL %d\n\n", defaultTTL)

	for _, rec := range records {
		// Filter out NS records pointing at HE's own nameservers.
		// HE uses ns1.he.net through ns5.he.net. Filter pattern: content ends in ".he.net"
		// (after FQDN normalization). Using suffix match avoids hardcoding specific NS hostnames.
		if rec.Type == model.RecordTypeNS {
			normalized := strings.ToLower(strings.TrimSuffix(rec.Content, "."))
			if strings.HasSuffix(normalized, ".he.net") || normalized == "he.net" {
				continue
			}
		}

		rr, err := recordToRR(rec, zoneName)
		if err != nil {
			// Unsupported type — silently skip. Export only emits what it can represent.
			continue
		}
		fmt.Fprintln(&buf, rr.String())
	}

	return buf.String(), nil
}

// v1ExportTypes is the set of record types this export supports.
// Matches the v1RecordTypes set in internal/api/validate/records.go.
//
// WHY maintained separately here (not imported from validate):
//   bindio must not import validate, which is in the api subtree and imports api-specific
//   packages. Importing it would create an unexpected coupling between the pure data conversion
//   layer (bindio) and the HTTP API layer (validate). The duplication is intentional and
//   the two maps must stay in sync manually.
//
// DEPENDENCY: if a new type is added to v1RecordTypes in internal/api/validate/records.go,
// it must also be added here for export/import to support it.
var v1ExportTypes = map[model.RecordType]bool{
	model.RecordTypeA:     true,
	model.RecordTypeAAAA:  true,
	model.RecordTypeCNAME: true,
	model.RecordTypeMX:    true,
	model.RecordTypeTXT:   true,
	model.RecordTypeSRV:   true,
	model.RecordTypeCAA:   true,
	model.RecordTypeNS:    true,
}

// recordToRR converts a model.Record to a dns.RR concrete type for miekg/dns serialization.
//
// WHY all RR_Header.Name values must be FQDN (ending in '.'):
//   dns.RR.String() uses the FQDN form to produce valid zone file output. Non-FQDN
//   names cause output like "www A 93.184.216.34" instead of "www.example.com. A 93.184.216.34",
//   which is invalid in a zone file with $ORIGIN set. (RESEARCH.md Pitfall 1)
//
// HE scraper returns relative names (e.g. "www", "mail.example.com") — we normalize
// to FQDN by appending origin when needed.
func recordToRR(rec model.Record, origin string) (dns.RR, error) {
	if !v1ExportTypes[rec.Type] {
		return nil, fmt.Errorf("type %s not in v1 export set", rec.Type)
	}

	// Normalize record name to FQDN. HE scraper returns relative names (e.g. "www").
	name := rec.Name
	if !dns.IsFqdn(name) {
		// Relative name: append origin to form FQDN.
		name = dns.Fqdn(name + "." + origin)
	}

	hdr := dns.RR_Header{
		Name:  name,
		Class: dns.ClassINET,
		Ttl:   uint32(rec.TTL),
	}

	switch rec.Type {
	case model.RecordTypeA:
		hdr.Rrtype = dns.TypeA
		return &dns.A{Hdr: hdr, A: net.ParseIP(rec.Content).To4()}, nil

	case model.RecordTypeAAAA:
		hdr.Rrtype = dns.TypeAAAA
		return &dns.AAAA{Hdr: hdr, AAAA: net.ParseIP(rec.Content)}, nil

	case model.RecordTypeCNAME:
		// WHY field is Target not Cname:
		//   miekg/dns v1.1.x dns.CNAME struct uses "Target" for the canonical name field
		//   (matching the RDATA field name in RFC 1035). The field was never named "Cname".
		hdr.Rrtype = dns.TypeCNAME
		return &dns.CNAME{Hdr: hdr, Target: dns.Fqdn(rec.Content)}, nil

	case model.RecordTypeMX:
		hdr.Rrtype = dns.TypeMX
		return &dns.MX{Hdr: hdr, Preference: uint16(rec.Priority), Mx: dns.Fqdn(rec.Content)}, nil

	case model.RecordTypeTXT:
		// WHY TXT content is wrapped as single-element []string:
		//   TXT content from HE is a single string. miekg/dns Txt field is []string where
		//   each element is a 255-char segment. For single-valued TXT, wrap as single-element slice.
		//   Do NOT split manually — miekg/dns handles chunking in String() output.
		//   (RESEARCH.md anti-pattern: manual splitting produces doubled-quoted segments)
		hdr.Rrtype = dns.TypeTXT
		return &dns.TXT{Hdr: hdr, Txt: []string{rec.Content}}, nil

	case model.RecordTypeNS:
		hdr.Rrtype = dns.TypeNS
		return &dns.NS{Hdr: hdr, Ns: dns.Fqdn(rec.Content)}, nil

	case model.RecordTypeSRV:
		hdr.Rrtype = dns.TypeSRV
		return &dns.SRV{
			Hdr:      hdr,
			Priority: uint16(rec.Priority),
			Weight:   uint16(rec.Weight),
			Port:     uint16(rec.Port),
			Target:   dns.Fqdn(rec.Target),
		}, nil

	case model.RecordTypeCAA:
		// WHY CAA content must be split into separate fields:
		//   CAA content is stored as "flags tag value" (e.g. "0 issue letsencrypt.org").
		//   miekg/dns CAA struct has separate Flag uint8, Tag string, Value string fields.
		//   Must split before constructing the RR. Passing the full string as Value
		//   produces an invalid zone file line. (RESEARCH.md Pitfall 2)
		parts := strings.Fields(rec.Content)
		if len(parts) < 3 {
			return nil, fmt.Errorf("CAA content %q has fewer than 3 fields", rec.Content)
		}
		flags, err := strconv.ParseUint(parts[0], 10, 8)
		if err != nil {
			return nil, fmt.Errorf("CAA flag parse error: %w", err)
		}
		hdr.Rrtype = dns.TypeCAA
		return &dns.CAA{Hdr: hdr, Flag: uint8(flags), Tag: parts[1], Value: parts[2]}, nil

	default:
		return nil, fmt.Errorf("unsupported record type: %s", rec.Type)
	}
}
