package explorer

import (
	"context"
	"database/sql"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/weigong/dbbackups/internal/domain"
)

func TestReadOnlyRoleSeesPrimaryKey(t *testing.T) {
	if os.Getenv("EXPLORER_TEST_ENGINE") != "postgres" {
		t.Skip("requires isolated PostgreSQL fixtures")
	}
	port, err := strconv.Atoi(os.Getenv("EXPLORER_TEST_PORT"))
	if err != nil {
		t.Fatal(err)
	}
	target := &domain.Target{Engine: "postgres", Host: "127.0.0.1", Port: port, DBName: "feature_source", Username: "feature_ro", SSLMode: "disable"}
	structure, err := Describe(context.Background(), target, os.Getenv("EXPLORER_TEST_PASSWORD"), "public", "orders")
	if err != nil {
		t.Fatal(err)
	}
	if len(structure.Columns) == 0 || !structure.Columns[0].Primary {
		t.Fatalf("primary key invisible to read-only role: %+v", structure.Columns)
	}
}

func TestDatabaseReadOnlyTransaction(t *testing.T) {
	engine := os.Getenv("EXPLORER_TEST_ENGINE")
	if engine == "" {
		t.Skip("set EXPLORER_TEST_ENGINE for an isolated integration database")
	}
	port, err := strconv.Atoi(os.Getenv("EXPLORER_TEST_PORT"))
	if err != nil {
		t.Fatal(err)
	}
	target := &domain.Target{Engine: engine, Host: "127.0.0.1", Port: port, DBName: "feature_source", Username: os.Getenv("EXPLORER_TEST_USER"), SSLMode: "disable"}
	err = readOnly(context.Background(), target, os.Getenv("EXPLORER_TEST_PASSWORD"), func(ctx context.Context, tx *sql.Tx) error {
		if engine == "postgres" {
			var mode string
			if e := tx.QueryRowContext(ctx, "SHOW transaction_read_only").Scan(&mode); e != nil {
				return e
			}
			if mode != "on" {
				t.Errorf("transaction_read_only=%s", mode)
			}
		}
		_, e := tx.ExecContext(ctx, "UPDATE orders SET amount=amount WHERE id=1")
		if e == nil {
			t.Error("database accepted UPDATE in a read-only transaction")
		} else if text := strings.ToLower(e.Error()); !strings.Contains(text, "read-only") && !strings.Contains(text, "read only") {
			t.Errorf("expected database read-only rejection, got %v", e)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
