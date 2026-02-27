package pages

import (
	"fmt"
	"strconv"

	playwright "github.com/playwright-community/playwright-go"
	"github.com/vnovakov/dns-he-net-automation/internal/model"
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
	rowSelector := fmt.Sprintf("tr#%s", recordID)
	if err := rp.page.Locator(rowSelector).Click(); err != nil {
		return fmt.Errorf("click record row %q: %w", recordID, err)
	}

	if err := rp.page.Locator(SelectorRecordForm).WaitFor(); err != nil {
		return fmt.Errorf("wait for record form after clicking row %q: %w", recordID, err)
	}

	return nil
}

// DeleteRecord triggers the JavaScript deleteRecord function for the given record,
// then waits for the page to reload (confirming the deletion was submitted).
func (rp *RecordFormPage) DeleteRecord(recordID, zoneName, recordType string) error {
	// Call the page's deleteRecord JS function with the three required arguments.
	_, err := rp.page.Evaluate("([id, zone, rtype]) => deleteRecord(id, zone, rtype)",
		[]interface{}{recordID, zoneName, recordType})
	if err != nil {
		return fmt.Errorf("call deleteRecord(%q, %q, %q): %w", recordID, zoneName, recordType, err)
	}

	if err := rp.page.WaitForLoadState(playwright.PageWaitForLoadStateOptions{
		State: playwright.LoadStateNetworkidle,
	}); err != nil {
		return fmt.Errorf("wait for load state after deleteRecord: %w", err)
	}

	return nil
}
