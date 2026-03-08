package pages

import (
	"errors"
	"fmt"

	playwright "github.com/playwright-community/playwright-go"
)

// LoginPage provides browser automation for the dns.he.net login flow.
type LoginPage struct {
	page playwright.Page
}

// NewLoginPage creates a LoginPage backed by the given Playwright page.
func NewLoginPage(page playwright.Page) *LoginPage {
	return &LoginPage{page: page}
}

// Login navigates to dns.he.net, fills in the credentials, submits the form,
// and verifies that the logout link is visible (confirming a successful login).
//
// SECURITY (SEC-03): Credentials are never included in error messages.
func (lp *LoginPage) Login(username, password string) error {
	if _, err := lp.page.Goto("https://dns.he.net/"); err != nil {
		return fmt.Errorf("navigate to dns.he.net: %w", err)
	}

	if err := lp.page.WaitForLoadState(playwright.PageWaitForLoadStateOptions{
		State: playwright.LoadStateNetworkidle,
	}); err != nil {
		return fmt.Errorf("wait for load state: %w", err)
	}

	if err := lp.page.Locator(SelectorLoginEmail).Fill(username); err != nil {
		return fmt.Errorf("fill email field: %w", err)
	}

	if err := lp.page.Locator(SelectorLoginPassword).Fill(password); err != nil {
		return fmt.Errorf("fill password field: %w", err)
	}

	if err := lp.page.Locator(SelectorLoginSubmit).Click(); err != nil {
		return fmt.Errorf("click login button: %w", err)
	}

	if err := lp.page.WaitForLoadState(playwright.PageWaitForLoadStateOptions{
		State: playwright.LoadStateNetworkidle,
	}); err != nil {
		return fmt.Errorf("wait for post-login load state: %w", err)
	}

	visible, err := lp.page.Locator(SelectorLogoutLink).IsVisible()
	if err != nil {
		return fmt.Errorf("check logout link visibility: %w", err)
	}
	if !visible {
		// Do NOT include username or password in the error — SEC-03.
		return errors.New("login failed: logout link not found after login attempt")
	}

	return nil
}

// IsLoggedIn navigates to dns.he.net and checks whether the logout link is visible,
// indicating an active authenticated session.
func (lp *LoginPage) IsLoggedIn() (bool, error) {
	if _, err := lp.page.Goto("https://dns.he.net/"); err != nil {
		return false, fmt.Errorf("navigate to dns.he.net: %w", err)
	}

	if err := lp.page.WaitForLoadState(playwright.PageWaitForLoadStateOptions{
		State: playwright.LoadStateNetworkidle,
	}); err != nil {
		return false, fmt.Errorf("wait for load state: %w", err)
	}

	visible, err := lp.page.Locator(SelectorLogoutLink).IsVisible()
	if err != nil {
		return false, fmt.Errorf("check logout link visibility: %w", err)
	}

	return visible, nil
}
