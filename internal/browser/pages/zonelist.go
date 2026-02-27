package pages

import (
	"fmt"

	playwright "github.com/playwright-community/playwright-go"
	"github.com/vnovakov/dns-he-net-automation/internal/model"
)

const heNetBaseURL = "https://dns.he.net/"

// ZoneListPage provides browser automation for the dns.he.net zone list view.
type ZoneListPage struct {
	page playwright.Page
}

// NewZoneListPage creates a ZoneListPage backed by the given Playwright page.
func NewZoneListPage(page playwright.Page) *ZoneListPage {
	return &ZoneListPage{page: page}
}

// NavigateToZoneList navigates to dns.he.net and waits for the domains table to appear.
func (zp *ZoneListPage) NavigateToZoneList() error {
	if _, err := zp.page.Goto(heNetBaseURL); err != nil {
		return fmt.Errorf("navigate to zone list: %w", err)
	}

	if err := zp.page.WaitForLoadState(playwright.PageWaitForLoadStateOptions{
		State: playwright.LoadStateNetworkidle,
	}); err != nil {
		return fmt.Errorf("wait for load state: %w", err)
	}

	if err := zp.page.Locator(SelectorDomainsTable).WaitFor(); err != nil {
		return fmt.Errorf("wait for domains table: %w", err)
	}

	return nil
}

// ListZones returns all DNS zones visible in the zone list.
// Zone IDs are extracted from the delete-image value attribute,
// zone names from the delete-image name attribute.
func (zp *ZoneListPage) ListZones() ([]model.Zone, error) {
	deleteImgs := zp.page.Locator(SelectorZoneDeleteImg)

	count, err := deleteImgs.Count()
	if err != nil {
		return nil, fmt.Errorf("count zone delete images: %w", err)
	}

	zones := make([]model.Zone, 0, count)
	for i := 0; i < count; i++ {
		img := deleteImgs.Nth(i)

		name, err := img.GetAttribute("name")
		if err != nil {
			return nil, fmt.Errorf("get zone name from delete img[%d]: %w", i, err)
		}

		value, err := img.GetAttribute("value")
		if err != nil {
			return nil, fmt.Errorf("get zone ID from delete img[%d]: %w", i, err)
		}

		zones = append(zones, model.Zone{
			ID:   value,
			Name: name,
		})
	}

	return zones, nil
}

// GetZoneID returns the zone ID for the zone with the given name.
// Uses the delete-image selector with a name attribute filter for direct lookup.
func (zp *ZoneListPage) GetZoneID(zoneName string) (string, error) {
	selector := fmt.Sprintf(`img[alt="delete"][name="%s"]`, zoneName)
	img := zp.page.Locator(selector)

	value, err := img.GetAttribute("value")
	if err != nil {
		return "", fmt.Errorf("get zone ID for %q: %w", zoneName, err)
	}
	if value == "" {
		return "", fmt.Errorf("zone %q not found or has no ID", zoneName)
	}

	return value, nil
}

// NavigateToZone navigates to the DNS record management page for the given zone ID
// and waits for at least one record row to appear.
func (zp *ZoneListPage) NavigateToZone(zoneID string) error {
	url := fmt.Sprintf("%s?hosted_dns_zoneid=%s&menu=edit_zone&hosted_dns_editzone", heNetBaseURL, zoneID)

	if _, err := zp.page.Goto(url); err != nil {
		return fmt.Errorf("navigate to zone %s: %w", zoneID, err)
	}

	if err := zp.page.WaitForLoadState(playwright.PageWaitForLoadStateOptions{
		State: playwright.LoadStateNetworkidle,
	}); err != nil {
		return fmt.Errorf("wait for load state on zone %s: %w", zoneID, err)
	}

	// Wait for either an editable record row or a locked row (SOA) to appear.
	// Using a combined selector — at least one of these must be present.
	combinedSelector := fmt.Sprintf("%s, %s", SelectorRecordRow, SelectorRecordRowLocked)
	if err := zp.page.Locator(combinedSelector).First().WaitFor(); err != nil {
		return fmt.Errorf("wait for record rows in zone %s: %w", zoneID, err)
	}

	return nil
}

// RecordRow represents a single row in the zone records table.
type RecordRow struct {
	ID          string
	DisplayText string
	IsLocked    bool
}

// GetRecordRows returns all record rows for the current zone page,
// including both editable rows (tr.dns_tr) and locked rows (tr.dns_tr_locked).
func (zp *ZoneListPage) GetRecordRows() ([]RecordRow, error) {
	var rows []RecordRow

	// Collect editable rows.
	editableRows := zp.page.Locator(SelectorRecordRow)
	editableCount, err := editableRows.Count()
	if err != nil {
		return nil, fmt.Errorf("count editable record rows: %w", err)
	}

	for i := 0; i < editableCount; i++ {
		row := editableRows.Nth(i)

		id, err := row.GetAttribute("id")
		if err != nil {
			return nil, fmt.Errorf("get ID for editable row[%d]: %w", i, err)
		}

		text, err := row.InnerText()
		if err != nil {
			return nil, fmt.Errorf("get text for editable row[%d]: %w", i, err)
		}

		rows = append(rows, RecordRow{
			ID:          id,
			DisplayText: text,
			IsLocked:    false,
		})
	}

	// Collect locked rows (e.g., SOA).
	lockedRows := zp.page.Locator(SelectorRecordRowLocked)
	lockedCount, err := lockedRows.Count()
	if err != nil {
		return nil, fmt.Errorf("count locked record rows: %w", err)
	}

	for i := 0; i < lockedCount; i++ {
		row := lockedRows.Nth(i)

		id, err := row.GetAttribute("id")
		if err != nil {
			return nil, fmt.Errorf("get ID for locked row[%d]: %w", i, err)
		}

		text, err := row.InnerText()
		if err != nil {
			return nil, fmt.Errorf("get text for locked row[%d]: %w", i, err)
		}

		rows = append(rows, RecordRow{
			ID:          id,
			DisplayText: text,
			IsLocked:    true,
		})
	}

	return rows, nil
}
