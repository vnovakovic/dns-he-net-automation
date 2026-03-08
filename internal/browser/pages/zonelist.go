package pages

import (
	"fmt"
	"log/slog"
	"strconv"
	"strings"

	playwright "github.com/playwright-community/playwright-go"
	"github.com/vnovakovic/dns-he-net-automation/internal/model"
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

// AddZone navigates to the zone list, opens the add-zone panel, fills in the domain
// name, submits the form, and returns the newly created zone's ID.
func (zp *ZoneListPage) AddZone(domainName string) (string, error) {
	if err := zp.NavigateToZoneList(); err != nil {
		return "", err
	}

	if err := zp.page.Locator(SelectorAddZoneTrigger).Click(); err != nil {
		return "", fmt.Errorf("add zone %q: click trigger: %w", domainName, err)
	}

	if err := zp.page.Locator(SelectorAddZonePanel).WaitFor(); err != nil {
		return "", fmt.Errorf("add zone %q: wait for panel: %w", domainName, err)
	}

	if err := zp.page.Locator(SelectorAddZoneInput).Fill(domainName); err != nil {
		return "", fmt.Errorf("add zone %q: fill domain name: %w", domainName, err)
	}

	if err := zp.page.Locator(SelectorAddZoneSubmit).Click(); err != nil {
		return "", fmt.Errorf("add zone %q: click submit: %w", domainName, err)
	}

	if err := zp.page.WaitForLoadState(playwright.PageWaitForLoadStateOptions{
		State: playwright.LoadStateNetworkidle,
	}); err != nil {
		return "", fmt.Errorf("add zone %q: wait for network idle: %w", domainName, err)
	}

	zoneID, err := zp.GetZoneID(domainName)
	if err != nil {
		return "", fmt.Errorf("add zone %q: look up new zone ID: %w", domainName, err)
	}

	return zoneID, nil
}

// DeleteZone deletes the zone with the given zoneID and zoneName from dns.he.net.
// It registers a dialog handler BEFORE clicking the delete image, which is required
// because dns.he.net uses prompt() (not confirm()) — the handler fills "DELETE" and
// accepts the prompt. After deletion, it verifies the zone is no longer present.
func (zp *ZoneListPage) DeleteZone(zoneID string, zoneName string) error {
	if err := zp.NavigateToZoneList(); err != nil {
		return err
	}

	// Register dialog handler BEFORE the click (CRITICAL).
	// dns.he.net calls prompt() which fires synchronously on click.
	// The handler must be registered pre-emptively.
	// playwright-go v0.5700.1: use OnDialog, not On("dialog").
	// Dialog.Accept(promptText ...string) accepts the prompt with the given text.
	zp.page.OnDialog(func(dialog playwright.Dialog) {
		_ = dialog.Accept("DELETE")
	})

	selector := fmt.Sprintf(`img[alt="delete"][value="%s"]`, zoneID)
	if err := zp.page.Locator(selector).Click(); err != nil {
		return fmt.Errorf("delete zone %q: click delete image: %w", zoneName, err)
	}

	if err := zp.page.WaitForLoadState(playwright.PageWaitForLoadStateOptions{
		State: playwright.LoadStateNetworkidle,
	}); err != nil {
		return fmt.Errorf("delete zone %q: wait for network idle: %w", zoneName, err)
	}

	// Verify deletion by checking that the zone is no longer present.
	remainingID, err := zp.GetZoneID(zoneName)
	if err != nil {
		// GetZoneID returned an error — zone not found, deletion succeeded.
		return nil
	}
	if remainingID != "" {
		return fmt.Errorf("delete zone %q: zone still present after deletion", zoneName)
	}
	return nil
}

// GetZoneName returns the domain name for the zone with the given zoneID.
// It looks up the img[alt="delete"][value=ZONE_ID] element and reads its name attribute.
func (zp *ZoneListPage) GetZoneName(zoneID string) (string, error) {
	selector := fmt.Sprintf(`img[alt="delete"][value="%s"]`, zoneID)
	name, err := zp.page.Locator(selector).GetAttribute("name")
	if err != nil {
		return "", fmt.Errorf("get zone name for ID %q: %w", zoneID, err)
	}
	return name, nil
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
// including editable rows (tr.dns_tr), dynamic rows (tr.dns_tr_dynamic), and locked rows (tr.dns_tr_locked).
//
// WHY three selectors:
//
//	HE.net uses class="dns_tr" for static records and class="dns_tr_dynamic" for DDNS records.
//	A single CSS selector cannot match both without resorting to :is() which Playwright may
//	not support on all versions. Querying separately keeps each call simple and explicit.
//	DISCOVERED: 2026-03-03 by dumping live page HTML — dynamic records were entirely invisible
//	before SelectorRecordRowDynamic was added.
func (zp *ZoneListPage) GetRecordRows() ([]RecordRow, error) {
	var rows []RecordRow

	// collectEditable queries rows by selector and appends them as non-locked.
	collectEditable := func(selector string) error {
		locator := zp.page.Locator(selector)
		count, err := locator.Count()
		if err != nil {
			return fmt.Errorf("count rows (%s): %w", selector, err)
		}
		for i := 0; i < count; i++ {
			row := locator.Nth(i)
			id, err := row.GetAttribute("id")
			if err != nil {
				return fmt.Errorf("get ID for row[%d] (%s): %w", i, selector, err)
			}
			text, err := row.InnerText()
			if err != nil {
				return fmt.Errorf("get text for row[%d] (%s): %w", i, selector, err)
			}
			rows = append(rows, RecordRow{ID: id, DisplayText: text, IsLocked: false})
		}
		return nil
	}

	if err := collectEditable(SelectorRecordRow); err != nil {
		return nil, err
	}
	if err := collectEditable(SelectorRecordRowDynamic); err != nil {
		return nil, err
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

// ParseRecordRow extracts a model.Record from a single tr.dns_tr row by its HTML element ID.
//
// Column indices for tr.dns_tr td elements (10 tds per row — verified against live dns.he.net):
//
//	td[0]: hidden — zone ID (ignored; supplied separately)
//	td[1]: hidden — record ID (dns.he.net internal ID)
//	td[2]: class="dns_view" — record name
//	td[3]: record type string (e.g., "A", "MX", "SRV")
//	td[4]: TTL as string (e.g., "300", "3600")
//	td[5]: priority — "-" for non-MX/SRV, numeric string for MX/SRV
//	td[6]: content/data — for SRV holds "Weight Port Target" space-separated
//	td[7]: hidden — DDNS flag ("0" or "1")
//	td[8]: DDNS key button (empty for static records)
//	td[9]: class="dns_delete" — delete img button (ignored)
func (zp *ZoneListPage) ParseRecordRow(rowID string) (*model.Record, error) {
	// WHY attribute selector instead of tr#ID:
	//   HE.net record row IDs are purely numeric (e.g. "5606222228"). CSS ID selectors
	//   require the identifier to start with a letter or underscore — a leading digit is a
	//   syntax error. tr#5606222228 fails with "not a valid selector" even though the HTML
	//   id attribute is valid. Using tr[id="..."] is the correct workaround for numeric IDs.
	cells := zp.page.Locator(fmt.Sprintf(`tr[id="%s"] td`, rowID))

	count, err := cells.Count()
	if err != nil {
		return nil, fmt.Errorf("parse record row %q: count tds: %w", rowID, err)
	}
	if count != 10 {
		return nil, fmt.Errorf("parse record row %q: expected 10 tds, got %d", rowID, count)
	}

	getText := func(idx int) (string, error) {
		text, err := cells.Nth(idx).InnerText()
		if err != nil {
			return "", fmt.Errorf("td[%d]: %w", idx, err)
		}
		return strings.TrimSpace(text), nil
	}

	recordID, err := getText(1)
	if err != nil {
		return nil, fmt.Errorf("parse record row %q: read record ID: %w", rowID, err)
	}

	name, err := getText(2)
	if err != nil {
		return nil, fmt.Errorf("parse record row %q: read name: %w", rowID, err)
	}

	recType, err := getText(3)
	if err != nil {
		return nil, fmt.Errorf("parse record row %q: read type: %w", rowID, err)
	}

	ttlStr, err := getText(4)
	if err != nil {
		return nil, fmt.Errorf("parse record row %q: read TTL: %w", rowID, err)
	}

	prioStr, err := getText(5)
	if err != nil {
		return nil, fmt.Errorf("parse record row %q: read priority: %w", rowID, err)
	}

	content, err := getText(6)
	if err != nil {
		return nil, fmt.Errorf("parse record row %q: read content: %w", rowID, err)
	}

	ddnsStr, err := getText(7)
	if err != nil {
		return nil, fmt.Errorf("parse record row %q: read DDNS flag: %w", rowID, err)
	}

	ttl, err := strconv.Atoi(ttlStr)
	if err != nil {
		return nil, fmt.Errorf("parse record row %q: parse TTL %q: %w", rowID, ttlStr, err)
	}

	var priority int
	if strings.TrimSpace(prioStr) == "-" {
		priority = 0
	} else {
		priority, err = strconv.Atoi(prioStr)
		if err != nil {
			return nil, fmt.Errorf("parse record row %q: parse priority %q: %w", rowID, prioStr, err)
		}
	}

	dynamic := strings.TrimSpace(ddnsStr) == "1"

	rec := &model.Record{
		ID:       recordID,
		Type:     model.RecordType(recType),
		Name:     name,
		TTL:      ttl,
		Priority: priority,
		Dynamic:  dynamic,
	}

	// SRV records: content field holds "Weight Port Target" space-separated.
	if rec.Type == model.RecordTypeSRV {
		parts := strings.Fields(content)
		if len(parts) == 3 {
			rec.Weight, _ = strconv.Atoi(parts[0])
			rec.Port, _ = strconv.Atoi(parts[1])
			rec.Target = parts[2]
			rec.Content = "" // SRV has no Content field
		} else {
			// Fallback: store raw content for debugging
			rec.Content = content
		}
	} else {
		rec.Content = content
	}

	// TXT records: HE.net wraps the content in double-quotes in the table row
	// (zone-file convention, e.g., "v=spf1 ~all"). Strip them so the stored
	// content matches what callers submit and expect.
	//
	// WHY this block is AFTER the SRV/else content assignment (not before):
	//   rec.Content is NOT set in the struct literal above — it starts as "".
	//   The original code placed this block before the else assignment, so it
	//   always checked len("") >= 2 → false → skipped stripping. Content was then
	//   assigned with quotes still present. The post-create FindRecord comparison:
	//     strings.EqualFold("\"v=spf1 ~all\"", "v=spf1 ~all") == false
	//   caused every TXT create to return browser_error even though the record was
	//   visible on dns.he.net. Moved AFTER the else block so rec.Content is set first.
	//   DISCOVERED: 2026-03-06 via code-order audit triggered by user TXT create failure.
	//
	// WHY only strip when BOTH sides are quotes (not strings.Trim):
	//   Multi-chunk TXT like "chunk1" "chunk2" has first='"' and last='"', so it
	//   would also strip → chunk1" "chunk2. That is still wrong for multi-chunk,
	//   but it is the same result as before. Single-value TXT (the common case)
	//   is correctly unquoted. Multi-chunk TXT is a known limitation (TODO).
	if rec.Type == model.RecordTypeTXT &&
		len(rec.Content) >= 2 &&
		rec.Content[0] == '"' &&
		rec.Content[len(rec.Content)-1] == '"' {
		rec.Content = rec.Content[1 : len(rec.Content)-1]
	}

	return rec, nil
}

// ListRecords navigates to the zone's record page and returns all manageable
// (non-locked) DNS records. Locked rows (e.g., SOA) are silently skipped.
func (zp *ZoneListPage) ListRecords(zoneID string) ([]model.Record, error) {
	if err := zp.NavigateToZone(zoneID); err != nil {
		return nil, fmt.Errorf("list records: %w", err)
	}

	rows, err := zp.GetRecordRows()
	if err != nil {
		return nil, fmt.Errorf("list records: get record rows: %w", err)
	}

	var results []model.Record
	for _, row := range rows {
		if row.IsLocked {
			continue
		}
		rec, err := zp.ParseRecordRow(row.ID)
		if err != nil {
			slog.Warn("skip record row parse error", "row_id", row.ID, "err", err)
			continue
		}
		rec.ZoneID = zoneID
		results = append(results, *rec)
	}

	return results, nil
}

// FindRecord returns the record ID of the first existing record that matches
// the given record by type+name and type-specific content fields.
// Returns "" (with nil error) when no match is found.
// Used for idempotency checking before creating a new record.
func (zp *ZoneListPage) FindRecord(zoneID string, rec model.Record) (string, error) {
	all, err := zp.ListRecords(zoneID)
	if err != nil {
		return "", err
	}

	for _, existing := range all {
		if recordsMatch(existing, rec) {
			return existing.ID, nil
		}
	}

	return "", nil
}

// recordsMatch returns true when two records are considered identical for idempotency purposes.
// Matching is type-specific:
//   - Dynamic A/AAAA: Type + Name only (content NOT compared — see WHY below)
//   - MX:  Type + Name (case-insensitive) + Content (case-insensitive) + Priority
//   - SRV: Type + Name (case-insensitive) + Priority + Weight + Port + Target (case-insensitive)
//   - All others: Type + Name (case-insensitive) + Content (case-insensitive)
//
// WHY skip content for dynamic A/AAAA:
//
//	When a dynamic A/AAAA record is created via the HE.net form, HE.net IGNORES
//	the submitted IP and instead sets content to the requester's current public IP.
//	Comparing content would therefore never match: we submit "1.2.3.4" but HE.net
//	stores "47.73.x.x". Type+name match is correct and safe because HE.net limits
//	each hostname to one dynamic A record.
//
//	PREVIOUSLY: content was always compared, causing both the idempotency pre-check
//	and the post-create ID lookup to fail for every dynamic A/AAAA record.
//	DISCOVERED: 2026-03-03 via debug screenshot showing the record was created with
//	the server's current IP, not the submitted IP.
func recordsMatch(a, b model.Record) bool {
	if a.Type != b.Type {
		return false
	}
	if !strings.EqualFold(a.Name, b.Name) {
		return false
	}
	// Dynamic A/AAAA: HE.net overwrites content — match by type+name only.
	if b.Dynamic && (a.Type == model.RecordTypeA || a.Type == model.RecordTypeAAAA) {
		return true
	}
	switch a.Type {
	case model.RecordTypeMX:
		return strings.EqualFold(a.Content, b.Content) && a.Priority == b.Priority
	case model.RecordTypeSRV:
		return a.Priority == b.Priority && a.Weight == b.Weight &&
			a.Port == b.Port && strings.EqualFold(a.Target, b.Target)
	default:
		return strings.EqualFold(a.Content, b.Content)
	}
}
