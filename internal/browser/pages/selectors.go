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
//
// WHY three separate row selectors:
//   dns.he.net uses distinct CSS classes for non-dynamic, dynamic, and locked (SOA) records.
//   All three must be collected to handle the full record set.
//   DISCOVERED: 2026-03-03 by dumping page HTML after NavigateToZone — dynamic A/AAAA records
//   use class="dns_tr_dynamic" and were being silently skipped before this selector was added.
const (
	SelectorRecordRow        = `tr.dns_tr`
	SelectorRecordRowDynamic = `tr.dns_tr_dynamic`
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

// Delete record form selectors.
//
// IMPORTANT — WHY we do NOT use Playwright locators for deletion:
//   dns.he.net's deleteRecord(id, zone, type) JS shows a native window.prompt()
//   to confirm deletion. In headless Playwright, window.prompt() auto-dismisses
//   with null — the record is never actually deleted.
//
//   form#record_delete has ONLY hidden inputs (no text input, no submit button).
//   The "Type DELETE to confirm" dialog is window.prompt(), not a DOM element.
//   Confirmed 2026-03-03 via HTML dump taken immediately after deleteRecord() call.
//
//   Deletion is performed by directly setting the hidden form inputs via
//   page.Evaluate() and calling document.forms['del_record'].submit().
//   See DeleteRecord() in recordform.go.
//
// SelectorDeleteRecordForm is kept for reference / future use but is not used
// in the automation flow.
const (
	SelectorDeleteRecordForm = `form#record_delete`
)

// DDNS key form selectors — appear when clicking img[alt="generate"] in the DDNS column.
// HE.net reuses form#edit_record for the DDNS key dialog; name="generate_key" distinguishes it.
// Verified against live dns.he.net DOM on 2026-03-03.
// DEPENDENCY: If HE.net changes these IDs, update here AND SetDDNSKey in recordform.go.
const (
	SelectorDDNSKeyForm   = `form#edit_record[name="generate_key"]`
	SelectorDDNSKeyInput  = `input#_key`
	SelectorDDNSKeyInput2 = `input#_key2`
	// WHY name="generate_key" not id="_hds":
	//   id="_hds" is shared with the regular record submit. The name attribute differs
	//   (generate_key vs hosted_dns_editrecord), making it the reliable distinguisher.
	SelectorDDNSKeySubmit = `input[name="generate_key"][value="Submit"]`
)

// Add zone form selectors.
const (
	SelectorAddZoneTrigger = `a[onclick*="add_zone"]`
	SelectorAddZonePanel   = `div#add_zone`
	SelectorAddZoneInput   = `input[name="add_domain"]`
	SelectorAddZoneSubmit  = `input[name="submit"][value="Add Domain!"]`
)
