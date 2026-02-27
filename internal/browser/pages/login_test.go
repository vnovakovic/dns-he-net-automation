package pages

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/vnovakov/dns-he-net-automation/internal/model"
)

// TestSelectorConstants verifies that all selector constants are non-empty strings.
// This is a compile-time + runtime guard against accidentally blank selectors.
func TestSelectorConstants(t *testing.T) {
	selectors := map[string]string{
		"SelectorLoginEmail":       SelectorLoginEmail,
		"SelectorLoginPassword":    SelectorLoginPassword,
		"SelectorLoginSubmit":      SelectorLoginSubmit,
		"SelectorLogoutLink":       SelectorLogoutLink,
		"SelectorDomainsTable":     SelectorDomainsTable,
		"SelectorZoneEditImg":      SelectorZoneEditImg,
		"SelectorZoneDeleteImg":    SelectorZoneDeleteImg,
		"SelectorRecordRow":        SelectorRecordRow,
		"SelectorRecordRowLocked":  SelectorRecordRowLocked,
		"SelectorRecordDeleteCell": SelectorRecordDeleteCell,
		"SelectorRecordForm":       SelectorRecordForm,
		"SelectorRecordType":       SelectorRecordType,
		"SelectorRecordZoneID":     SelectorRecordZoneID,
		"SelectorRecordID":         SelectorRecordID,
		"SelectorRecordName":       SelectorRecordName,
		"SelectorRecordContent":    SelectorRecordContent,
		"SelectorRecordTTL":        SelectorRecordTTL,
		"SelectorRecordPriority":   SelectorRecordPriority,
		"SelectorRecordWeight":     SelectorRecordWeight,
		"SelectorRecordPort":       SelectorRecordPort,
		"SelectorRecordTarget":     SelectorRecordTarget,
		"SelectorRecordDynamic":    SelectorRecordDynamic,
		"SelectorRecordSubmit":     SelectorRecordSubmit,
		"SelectorRecordCancel":     SelectorRecordCancel,
		"SelectorDeleteRecordForm": SelectorDeleteRecordForm,
		"SelectorAddZonePanel":     SelectorAddZonePanel,
		"SelectorAddZoneInput":     SelectorAddZoneInput,
		"SelectorAddZoneSubmit":    SelectorAddZoneSubmit,
	}

	for name, value := range selectors {
		assert.NotEmpty(t, value, "selector constant %s must not be empty", name)
	}
}

// TestRecordTypeConstants verifies that all 17 RecordType constants exist
// and match their expected string values.
func TestRecordTypeConstants(t *testing.T) {
	expected := map[model.RecordType]string{
		model.RecordTypeA:     "A",
		model.RecordTypeAAAA:  "AAAA",
		model.RecordTypeCNAME: "CNAME",
		model.RecordTypeALIAS: "ALIAS",
		model.RecordTypeMX:    "MX",
		model.RecordTypeNS:    "NS",
		model.RecordTypeTXT:   "TXT",
		model.RecordTypeCAA:   "CAA",
		model.RecordTypeAFSDB: "AFSDB",
		model.RecordTypeHINFO: "HINFO",
		model.RecordTypeRP:    "RP",
		model.RecordTypeLOC:   "LOC",
		model.RecordTypeNAPTR: "NAPTR",
		model.RecordTypePTR:   "PTR",
		model.RecordTypeSSHFP: "SSHFP",
		model.RecordTypeSPF:   "SPF",
		model.RecordTypeSRV:   "SRV",
	}

	assert.Len(t, expected, 17, "must have exactly 17 record type constants")

	for constant, expectedValue := range expected {
		assert.Equal(t, expectedValue, string(constant),
			"RecordType constant value mismatch")
	}
}
