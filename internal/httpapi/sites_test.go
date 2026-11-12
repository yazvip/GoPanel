package httpapi

import (
	"errors"
	"fmt"
	"testing"

	"gopanel/internal/sites"
	"gopanel/internal/sys"
)

func TestSiteNginxMissingMapping(t *testing.T) {
	// Apply wraps the validation failure twice: once with ErrApplyFailed and
	// once with the underlying sys error. Both must survive for errors.Is.
	err := fmt.Errorf("%w: %w", sites.ErrApplyFailed,
		fmt.Errorf("%w: install nginx", sys.ErrNginxMissing))

	mapped := mapSiteError(err)
	var apiErr *apiError
	if !errors.As(mapped, &apiErr) {
		t.Fatalf("error is not apiError: %T", mapped)
	}
	if apiErr.Status != 503 || apiErr.Code != "sites.nginx_missing" {
		t.Fatalf("unexpected mapping: %d %s", apiErr.Status, apiErr.Code)
	}

	rejected := mapSiteError(fmt.Errorf("%w: nginx: [emerg] unknown directive", sites.ErrApplyFailed))
	if !errors.As(rejected, &apiErr) {
		t.Fatalf("apply error is not apiError: %T", rejected)
	}
	if apiErr.Status != 422 || apiErr.Code != "sites.invalid" {
		t.Fatalf("unexpected fallback mapping: %d %s", apiErr.Status, apiErr.Code)
	}
}
