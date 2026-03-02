// Package pages provides Page Object implementations for dns.he.net browser automation.
// All CSS selectors are defined as named constants in this file.
//
// Selectors verified against live dns.he.net on 2026-02-27 via Playwright MCP.
// Updated 2026-03-03: SelectorRecordSubmit fixed for edit mode (see comment below).
// Do NOT use these selector strings outside this package — go through the Page Object methods.
package pages

// Login page selectors.
const (
	SelectorLoginEmail    = `input[name="email"]`
	SelectorLoginPassword = `input[name="pass"]`
	SelectorLoginSubmit   = `input[value="Login!"]` // input, NOT button
	SelectorLogoutLink    = `a[href="/?action=logout"]`
)

// Zone list selectors.
const (
	SelectorDomainsTable = `#domains_table`
	SelectorZoneEditImg  = `img[alt="edit"]`
	SelectorZoneDeleteImg = `img[alt="delete"]`
)

// Zone record row selectors.
const (
	SelectorRecordRow        = `tr.dns_tr`
	SelectorRecordRowLocked  = `tr.dns_tr_locked`
	SelectorRecordDeleteCell = `td.dns_delete`
)

// Record form selectors — shared for all 17 record types.
// Fields show/hide via JavaScript based on selected record type.
const (
	SelectorRecordForm     = `form#edit_record`
	SelectorRecordType     = `input#_type`
	SelectorRecordZoneID   = `input#_zoneid`
	SelectorRecordID       = `input#_recordid`
	SelectorRecordName     = `input#_name`
	SelectorRecordContent  = `input#_content`
	SelectorRecordTTL      = `select#_ttl`
	SelectorRecordPriority = `input#_prio`
	SelectorRecordWeight   = `input#_weight`
	SelectorRecordPort     = `input#_port`
	SelectorRecordTarget   = `input#_target`
	SelectorRecordDynamic  = `input#_dynamic`
	// WHY no [value="Submit"] constraint:
	//   The same input#_hds element is reused for both add and edit operations, but
	//   dns.he.net changes its value attribute at runtime via JavaScript:
	//     - Add mode:  value="Submit"
	//     - Edit mode: value="Update"
	//   Constraining by value="Submit" caused a 30s timeout on every UpdateRecord call
	//   because the selector never matched the edit-mode button. Matching by name alone
	//   is safe — there is only one input[name="hosted_dns_editrecord"] on the page.
	//
	//   PREVIOUSLY: `input[name="hosted_dns_editrecord"][value="Submit"]`
	//   BROKEN FOR: all edit/update operations (UpdateRecord handler)
	//   FIXED ON:   2026-03-03 after inspecting live DOM with Playwright MCP
	SelectorRecordSubmit   = `input[name="hosted_dns_editrecord"]`
	SelectorRecordCancel   = `input#btn_cancel`
)

// Delete record form selector.
const (
	SelectorDeleteRecordForm = `form#record_delete`
)

// Add zone form selectors.
const (
	SelectorAddZoneTrigger = `a[onclick*="add_zone"]`
	SelectorAddZonePanel   = `div#add_zone`
	SelectorAddZoneInput   = `input[name="add_domain"]`
	SelectorAddZoneSubmit  = `input[name="submit"][value="Add Domain!"]`
)
