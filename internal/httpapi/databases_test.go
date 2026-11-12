package httpapi

import (
	"errors"
	"fmt"
	"testing"

	"gopanel/internal/sys"
)

func TestDatabaseClientMissingMapping(t *testing.T) {
	err := fmt.Errorf("%w: install the mysql-client or mariadb-client package", sys.ErrDatabaseClientMissing)

	conn := mapDatabaseConnectionError(err)
	var apiErr *apiError
	if !errors.As(conn, &apiErr) {
		t.Fatalf("connection error is not apiError: %T", conn)
	}
	if apiErr.Status != 503 || apiErr.Code != "databases.client_missing" {
		t.Fatalf("unexpected connection mapping: %d %s", apiErr.Status, apiErr.Code)
	}

	gen := mapDatabaseError(err)
	if !errors.As(gen, &apiErr) {
		t.Fatalf("generic error is not apiError: %T", gen)
	}
	if apiErr.Status != 503 || apiErr.Code != "databases.client_missing" {
		t.Fatalf("unexpected generic mapping: %d %s", apiErr.Status, apiErr.Code)
	}

	other := mapDatabaseConnectionError(errors.New("connection refused"))
	if !errors.As(other, &apiErr) {
		t.Fatalf("other error is not apiError: %T", other)
	}
	if apiErr.Status != 502 || apiErr.Code != "databases.unreachable" {
		t.Fatalf("unexpected fallback mapping: %d %s", apiErr.Status, apiErr.Code)
	}
}
