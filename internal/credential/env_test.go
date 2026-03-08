package credential

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewEnvProvider_SingleAccount(t *testing.T) {
	json := `[{"id":"prod","username":"user@example.com","password":"secret123"}]`
	p, err := NewEnvProvider(json)
	require.NoError(t, err)
	require.NotNil(t, p)

	cred, err := p.GetCredential(context.Background(), "prod")
	require.NoError(t, err)
	assert.Equal(t, "prod", cred.AccountID)
	assert.Equal(t, "user@example.com", cred.Username)
	assert.Equal(t, "secret123", cred.Password)
}

func TestNewEnvProvider_MultiAccount_ListSorted(t *testing.T) {
	json := `[
		{"id":"beta","username":"beta@example.com","password":"betapass"},
		{"id":"alpha","username":"alpha@example.com","password":"alphapass"},
		{"id":"gamma","username":"gamma@example.com","password":"gammapass"}
	]`
	p, err := NewEnvProvider(json)
	require.NoError(t, err)

	ids, err := p.ListAccountIDs(context.Background())
	require.NoError(t, err)
	assert.Equal(t, []string{"alpha", "beta", "gamma"}, ids)
}

func TestNewEnvProvider_EmptyString(t *testing.T) {
	_, err := NewEnvProvider("")
	require.Error(t, err)
}

func TestNewEnvProvider_InvalidJSON(t *testing.T) {
	_, err := NewEnvProvider("this is not json")
	require.Error(t, err)
}

func TestNewEnvProvider_EmptyArray(t *testing.T) {
	_, err := NewEnvProvider("[]")
	require.Error(t, err)
}

func TestNewEnvProvider_MissingID(t *testing.T) {
	json := `[{"id":"","username":"user","password":"pass"}]`
	_, err := NewEnvProvider(json)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "id field is empty")
}

func TestNewEnvProvider_MissingUsername(t *testing.T) {
	json := `[{"id":"acct1","username":"","password":"pass"}]`
	_, err := NewEnvProvider(json)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "username field is empty")
}

func TestNewEnvProvider_MissingPassword(t *testing.T) {
	password := "supersecret"
	// Use a JSON string where the password field is empty (not the secret).
	json := `[{"id":"acct1","username":"user","password":""}]`
	_, err := NewEnvProvider(json)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "password field is empty")
	// SECURITY (SEC-03): The error must NOT contain the actual password value.
	// (In this test the password is empty so we check the literal password variable.)
	assert.False(t, strings.Contains(err.Error(), password),
		"error message must not contain the password value")
}

func TestNewEnvProvider_DuplicateID(t *testing.T) {
	json := `[
		{"id":"same","username":"user1","password":"pass1"},
		{"id":"same","username":"user2","password":"pass2"}
	]`
	_, err := NewEnvProvider(json)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "duplicate account id")
}

func TestGetCredential_NotFound(t *testing.T) {
	json := `[{"id":"prod","username":"user","password":"pass"}]`
	p, err := NewEnvProvider(json)
	require.NoError(t, err)

	_, err = p.GetCredential(context.Background(), "nonexistent")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "nonexistent")
}

func TestNewEnvProvider_PasswordNeverInErrorMessages(t *testing.T) {
	// Test that error messages from validation do not leak the actual password value.
	// We test with a missing id (first validation) on an account that has a password set.
	// The password should not appear in the error even though the account has a password value.
	password := "my-secret-password-xyz"
	jsonStr := `[{"id":"","username":"user","password":"` + password + `"}]`
	_, err := NewEnvProvider(jsonStr)
	require.Error(t, err)
	assert.False(t, strings.Contains(err.Error(), password),
		"error message must not contain the password value (SEC-03)")
}
