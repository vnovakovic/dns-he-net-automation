package pages

import (
	"fmt"
	"strconv"

	playwright "github.com/playwright-community/playwright-go"
	"github.com/vnovakovic/dns-he-net-automation/internal/model"
)

// RecordFormPage provides browser automation for the dns.he.net add/edit record form.
// All 17 DNS record types use the same form#edit_record; fields show/hide via JavaScript.
type RecordFormPage struct {
	page playwright.Page
}

// NewRecordFormPage creates a RecordFormPage backed by the given Playwright page.
func NewRecordFormPage(page playwright.Page) *RecordFormPage {
	return &RecordFormPage{page: page}
}

// OpenNewRecordForm triggers the JavaScript editFormHandler for the given record type,
// then waits for the record form to appear.
//
// recordType must be one of the 17 types: A, AAAA, CNAME, ALIAS, MX, NS, TXT,
// CAA, AFSDB, HINFO, RP, LOC, NAPTR, PTR, SSHFP, SPF, SRV.
func (rp *RecordFormPage) OpenNewRecordForm(recordType string) error {
	if _, err := rp.page.Evaluate("editFormHandler", recordType); err != nil {
		return fmt.Errorf("call editFormHandler(%q): %w", recordType, err)
	}

	if err := rp.page.Locator(SelectorRecordForm).WaitFor(); err != nil {
		return fmt.Errorf("wait for record form after editFormHandler(%q): %w", recordType, err)
	}

	return nil
}

// FillRecord populates the record form fields based on the record type.
//
// Field mapping by type:
//   - SRV:              Name + Priority + Weight + Port + Target (NO Content)
//   - MX:               Name + Priority + Content
//   - A, AAAA, TXT, AFSDB: Name + Content + optional Dynamic checkbox
//   - All others:       Name + Content
//   - All types:        TTL (select value as string, e.g. "300", "3600")
func (rp *RecordFormPage) FillRecord(rec model.Record) error {
	// Fill Name field (required for all types).
	if err := rp.page.Locator(SelectorRecordName).Fill(rec.Name); err != nil {
		return fmt.Errorf("fill Name: %w", err)
	}

	switch rec.Type {
	case model.RecordTypeSRV:
		if err := rp.page.Locator(SelectorRecordPriority).Fill(strconv.Itoa(rec.Priority)); err != nil {
			return fmt.Errorf("fill Priority (SRV): %w", err)
		}
		if err := rp.page.Locator(SelectorRecordWeight).Fill(strconv.Itoa(rec.Weight)); err != nil {
			return fmt.Errorf("fill Weight (SRV): %w", err)
		}
		if err := rp.page.Locator(SelectorRecordPort).Fill(strconv.Itoa(rec.Port)); err != nil {
			return fmt.Errorf("fill Port (SRV): %w", err)
		}
		if err := rp.page.Locator(SelectorRecordTarget).Fill(rec.Target); err != nil {
			return fmt.Errorf("fill Target (SRV): %w", err)
		}

	case model.RecordTypeMX:
		if err := rp.page.Locator(SelectorRecordPriority).Fill(strconv.Itoa(rec.Priority)); err != nil {
			return fmt.Errorf("fill Priority (MX): %w", err)
		}
		if err := rp.page.Locator(SelectorRecordContent).Fill(rec.Content); err != nil {
			return fmt.Errorf("fill Content (MX): %w", err)
		}

	case model.RecordTypeA, model.RecordTypeAAAA, model.RecordTypeTXT, model.RecordTypeAFSDB:
		if err := rp.page.Locator(SelectorRecordContent).Fill(rec.Content); err != nil {
			return fmt.Errorf("fill Content (%s): %w", rec.Type, err)
		}
		if rec.Dynamic {
			checked, err := rp.page.Locator(SelectorRecordDynamic).IsChecked()
			if err != nil {
				return fmt.Errorf("check Dynamic state (%s): %w", rec.Type, err)
			}
			if !checked {
				if err := rp.page.Locator(SelectorRecordDynamic).Click(); err != nil {
					return fmt.Errorf("click Dynamic checkbox (%s): %w", rec.Type, err)
				}
			}
		}

	default:
		// Standard types: CNAME, ALIAS, NS, CAA, HINFO, RP, LOC, NAPTR, PTR, SSHFP, SPF
		if err := rp.page.Locator(SelectorRecordContent).Fill(rec.Content); err != nil {
			return fmt.Errorf("fill Content (%s): %w", rec.Type, err)
		}
	}

	// Select TTL for all types.
	ttlStr := strconv.Itoa(rec.TTL)
	if _, err := rp.page.Locator(SelectorRecordTTL).SelectOption(playwright.SelectOptionValues{
		Values: &[]string{ttlStr},
	}); err != nil {
		return fmt.Errorf("select TTL %q: %w", ttlStr, err)
	}

	return nil
}

// SubmitRecord clicks the submit button and waits for the page to settle.
func (rp *RecordFormPage) SubmitRecord() error {
	if err := rp.page.Locator(SelectorRecordSubmit).Click(); err != nil {
		return fmt.Errorf("click submit: %w", err)
	}

	if err := rp.page.WaitForLoadState(playwright.PageWaitForLoadStateOptions{
		State: playwright.LoadStateNetworkidle,
	}); err != nil {
		return fmt.Errorf("wait for load state after submit: %w", err)
	}

	return nil
}

// FillAndSubmit is a convenience method that calls FillRecord followed by SubmitRecord.
func (rp *RecordFormPage) FillAndSubmit(rec model.Record) error {
	if err := rp.FillRecord(rec); err != nil {
		return err
	}
	return rp.SubmitRecord()
}

// EditExistingRecord clicks the record row with the given ID to open the edit form,
// then waits for the record form to appear.
func (rp *RecordFormPage) EditExistingRecord(recordID string) error {
	// WHY attribute selector instead of CSS ID selector (tr#RECORDID):
	//   HE.net record row IDs are purely numeric (e.g. "8900607586"). CSS ID selectors
	//   require the ID to begin with a letter — a leading digit is a parse error and
	//   Playwright throws "is not a valid selector". The attribute selector [id="..."]
	//   has no such restriction and works identically for all values.
	//
	//   SAME fix applied in ParseRecordRow in zonelist.go — keep both in sync.
	rowSelector := fmt.Sprintf(`tr[id="%s"]`, recordID)
	if err := rp.page.Locator(rowSelector).Click(); err != nil {
		return fmt.Errorf("click record row %q: %w", recordID, err)
	}

	if err := rp.page.Locator(SelectorRecordForm).WaitFor(); err != nil {
		return fmt.Errorf("wait for record form after clicking row %q: %w", recordID, err)
	}

	return nil
}

// DeleteRecord deletes the record with the given ID by directly filling the hidden
// delete form (form[name="del_record"]) and submitting it.
//
// WHY bypass deleteRecord() JS and directly fill + submit the form:
//   dns.he.net's deleteRecord(id, zone, type) shows a native window.prompt() asking
//   the user to type "DELETE" to confirm. In headless Playwright, window.prompt()
//   auto-dismisses with null — deleteRecord() receives null instead of "DELETE", the
//   condition fails, and the function exits WITHOUT submitting the form. The record
//   appears deleted in our logs (no JS error is thrown) but remains on dns.he.net.
//
//   Direct approach: skip the JS dialog entirely. Set the hidden inputs
//   (hosted_dns_recordid, hosted_dns_delconfirm) ourselves and call form.submit().
//   This is equivalent to what deleteRecord() would do AFTER the user confirmed.
//
// WHY form.submit() may return a "context destroyed" Evaluate error:
//   form.submit() triggers an immediate page navigation. Playwright's page.Evaluate()
//   can detect the execution context being destroyed mid-call and return an error even
//   though the submit succeeded. This is expected — we ignore that error and rely on
//   WaitForLoadState to confirm the page actually reloaded after the delete POST.
//
// WHY hosted_dns_delconfirm = "DELETE":
//   The server checks this field against "DELETE" before processing the deletion.
//   Setting it to "DELETE" mirrors exactly what the browser sends when a user manually
//   confirms in the prompt.
//
// DISCOVERY 2026-03-03: HTML dump (debug-delete-popup.html) of page after calling
//   deleteRecord() JS confirmed form#record_delete has ONLY hidden inputs — no text
//   input, no submit button. The visible "Type DELETE" prompt is window.prompt().
//
// PREVIOUS BROKEN APPROACH 1: Evaluate("deleteRecord(id, zone, rtype)") alone →
//   prompt auto-dismissed with null, form never submitted, record persisted.
// PREVIOUS BROKEN APPROACH 2: WaitFor("form#record_delete input[type='text']") →
//   timed out 30s — no text input exists in the form DOM.
//
// zoneName and recordType are accepted for API compatibility but not used; the form's
// hosted_dns_zoneid is already set by the server when the zone page loads.
func (rp *RecordFormPage) DeleteRecord(recordID, zoneName, recordType string) error {
	// Step 1: Fill the hidden inputs. This is a pure DOM mutation — no navigation.
	if _, err := rp.page.Evaluate(
		`([id]) => {
			document.getElementById('hosted_dns_recordid').value = id;
			document.getElementById('hosted_dns_delconfirm').value = 'DELETE';
		}`,
		[]interface{}{recordID},
	); err != nil {
		return fmt.Errorf("fill delete form inputs for record %q: %w", recordID, err)
	}

	// Step 2: Submit the form. Triggers a page navigation — Evaluate() may return
	// a "context destroyed" error even on success. Ignored intentionally.
	_, _ = rp.page.Evaluate(`() => document.forms['del_record'].submit()`, nil)

	// Step 3: Wait for the page to settle after the delete POST + redirect.
	if err := rp.page.WaitForLoadState(playwright.PageWaitForLoadStateOptions{
		State: playwright.LoadStateNetworkidle,
	}); err != nil {
		return fmt.Errorf("wait for load state after delete submit (record %q): %w", recordID, err)
	}

	return nil
}

// SetDDNSKey sets (or regenerates) the DDNS update password for a dynamic record.
// Must be called while the browser page is on the zone's record list.
//
// WHY separate from FillAndSubmit:
//
//	The DDNS key uses a separate overlay form (img[alt="generate"] → form#edit_record
//	with name="generate_key"). It cannot be set as part of the regular add/edit flow.
//
// WHY attribute selector for row: numeric record IDs are invalid in CSS id selectors.
//
//	Same fix as EditExistingRecord and ParseRecordRow. See selectors.go for DDNS selectors.
func (rp *RecordFormPage) SetDDNSKey(recordID, key string) error {
	genSelector := fmt.Sprintf(`tr[id="%s"] img[alt="generate"]`, recordID)
	if err := rp.page.Locator(genSelector).Click(); err != nil {
		return fmt.Errorf("click DDNS generate icon for record %q: %w", recordID, err)
	}
	if err := rp.page.Locator(SelectorDDNSKeyForm).WaitFor(); err != nil {
		return fmt.Errorf("wait for DDNS key form for record %q: %w", recordID, err)
	}
	if err := rp.page.Locator(SelectorDDNSKeyInput).Fill(key); err != nil {
		return fmt.Errorf("fill DDNS key for record %q: %w", recordID, err)
	}
	if err := rp.page.Locator(SelectorDDNSKeyInput2).Fill(key); err != nil {
		return fmt.Errorf("fill DDNS key confirmation for record %q: %w", recordID, err)
	}
	if err := rp.page.Locator(SelectorDDNSKeySubmit).Click(); err != nil {
		return fmt.Errorf("submit DDNS key form for record %q: %w", recordID, err)
	}
	if err := rp.page.WaitForLoadState(playwright.PageWaitForLoadStateOptions{
		State: playwright.LoadStateNetworkidle,
	}); err != nil {
		return fmt.Errorf("wait after DDNS key submit for record %q: %w", recordID, err)
	}
	return nil
}
