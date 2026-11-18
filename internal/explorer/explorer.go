package explorer

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/weigong/dbbackups/internal/domain"
	"github.com/weigong/dbbackups/internal/mysql"
	"github.com/weigong/dbbackups/internal/pg"
)

type Table struct {
	Schema string `json:"schema"`
	Name   string `json:"name"`
	Kind   string `json:"kind"`
}
type Column struct {
	Name     string `json:"name"`
	Type     string `json:"type"`
	Nullable bool   `json:"nullable"`
	Default  string `json:"default"`
	Primary  bool   `json:"primary"`
}
type Index struct {
	Name       string `json:"name"`
	Definition string `json:"definition"`
}
type Structure struct {
	Columns []Column `json:"columns"`
	Indexes []Index  `json:"indexes"`
}
type Result struct {
	Columns    []string `json:"columns"`
	Rows       [][]any  `json:"rows"`
	Truncated  bool     `json:"truncated"`
	DurationMS int64    `json:"duration_ms"`
}

func open(target *domain.Target, password string) (*sql.DB, error) {
	if target.Engine != "postgres" {
		return mysql.Open(target, password)
	}
	return (pg.ConnInfo{Host: target.Host, Port: target.Port, DBName: target.DBName, User: target.Username, Password: password, SSLMode: target.SSLMode}).Open()
}

func readOnly(ctx context.Context, target *domain.Target, password string, run func(context.Context, *sql.Tx) error) error {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	db, err := open(target, password)
	if err != nil {
		return err
	}
	defer db.Close()
	conn, err := db.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	if target.Engine != "postgres" {
		statement := "SET SESSION max_execution_time = 10000"
		if target.Engine == "mariadb" {
			statement = "SET SESSION max_statement_time = 10"
		}
		if _, err = conn.ExecContext(ctx, statement); err != nil {
			return err
		}
		var id uint64
		if err = conn.QueryRowContext(ctx, "SELECT CONNECTION_ID()").Scan(&id); err != nil {
			return err
		}
		done, stopped := make(chan struct{}), make(chan struct{})
		go func() {
			defer close(stopped)
			select {
			case <-done:
				return
			case <-ctx.Done():
			}
			control, e := open(target, password)
			if e != nil {
				return
			}
			defer control.Close()
			killCtx, stop := context.WithTimeout(context.Background(), 2*time.Second)
			defer stop()
			_, _ = control.ExecContext(killCtx, fmt.Sprintf("KILL QUERY %d", id))
		}()
		defer func() { close(done); <-stopped }()
	}
	tx, err := conn.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if target.Engine == "postgres" {
		for _, q := range []string{"SET LOCAL statement_timeout = '10s'", "SET LOCAL lock_timeout = '2s'", "SET LOCAL search_path = pg_catalog, public"} {
			if _, err = tx.ExecContext(ctx, q); err != nil {
				return err
			}
		}
	}
	return run(ctx, tx)
}

func Tables(ctx context.Context, target *domain.Target, password, filter string) ([]Table, error) {
	out := []Table{}
	err := readOnly(ctx, target, password, func(ctx context.Context, tx *sql.Tx) error {
		q := `SELECT table_schema, table_name, table_type FROM information_schema.tables WHERE table_schema = ? ORDER BY table_schema, table_name LIMIT 2000`
		args := []any{target.DBName}
		if target.Engine == "postgres" {
			q = `SELECT table_schema, table_name, table_type FROM information_schema.tables WHERE table_schema NOT IN ('pg_catalog','information_schema') AND table_schema NOT LIKE 'pg_toast%' ORDER BY table_schema, table_name LIMIT 2000`
			args = nil
		}
		rows, err := tx.QueryContext(ctx, q, args...)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var t Table
			if err := rows.Scan(&t.Schema, &t.Name, &t.Kind); err != nil {
				return err
			}
			if strings.Contains(strings.ToLower(t.Name), strings.ToLower(filter)) {
				out = append(out, t)
			}
		}
		return rows.Err()
	})
	return out, err
}

func validateTable(ctx context.Context, target *domain.Target, password, schema, table string) error {
	tables, err := Tables(ctx, target, password, "")
	if err != nil {
		return err
	}
	for _, t := range tables {
		if t.Schema == schema && t.Name == table {
			return nil
		}
	}
	return fmt.Errorf("表不存在或当前账号无权访问")
}

func Describe(ctx context.Context, target *domain.Target, password, schema, table string) (*Structure, error) {
	if err := validateTable(ctx, target, password, schema, table); err != nil {
		return nil, err
	}
	out := &Structure{Columns: []Column{}, Indexes: []Index{}}
	err := readOnly(ctx, target, password, func(ctx context.Context, tx *sql.Tx) error {
		q := `SELECT column_name, column_type, is_nullable = 'YES', COALESCE(column_default,''), column_key = 'PRI' FROM information_schema.columns WHERE table_schema = ? AND table_name = ? ORDER BY ordinal_position`
		if target.Engine == "postgres" {
			q = `SELECT c.column_name, c.data_type, c.is_nullable = 'YES', COALESCE(c.column_default,''), EXISTS (SELECT 1 FROM pg_catalog.pg_constraint pk JOIN pg_catalog.pg_class rel ON rel.oid=pk.conrelid JOIN pg_catalog.pg_namespace ns ON ns.oid=rel.relnamespace JOIN pg_catalog.pg_attribute a ON a.attrelid=rel.oid AND a.attnum=ANY(pk.conkey) WHERE pk.contype='p' AND ns.nspname=c.table_schema AND rel.relname=c.table_name AND a.attname=c.column_name) FROM information_schema.columns c WHERE c.table_schema=$1 AND c.table_name=$2 ORDER BY c.ordinal_position`
		}
		rows, err := tx.QueryContext(ctx, q, schema, table)
		if err != nil {
			return err
		}
		for rows.Next() {
			var c Column
			if err := rows.Scan(&c.Name, &c.Type, &c.Nullable, &c.Default, &c.Primary); err != nil {
				rows.Close()
				return err
			}
			out.Columns = append(out.Columns, c)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		q = `SELECT index_name, CONCAT(IF(non_unique=0,'UNIQUE ',''),index_type,' (',GROUP_CONCAT(COALESCE(column_name,'<expression>') ORDER BY seq_in_index SEPARATOR ', '),')') FROM information_schema.statistics WHERE table_schema=? AND table_name=? GROUP BY index_name,non_unique,index_type ORDER BY index_name`
		if target.Engine == "postgres" {
			q = `SELECT indexname, indexdef FROM pg_indexes WHERE schemaname=$1 AND tablename=$2 ORDER BY indexname`
		}
		rows, err = tx.QueryContext(ctx, q, schema, table)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var i Index
			if err := rows.Scan(&i.Name, &i.Definition); err != nil {
				return err
			}
			out.Indexes = append(out.Indexes, i)
		}
		return rows.Err()
	})
	return out, err
}

func Data(ctx context.Context, target *domain.Target, password string, request DataRequest) (*Result, error) {
	structure, err := Describe(ctx, target, password, request.Schema, request.Table)
	if err != nil {
		return nil, err
	}
	query, args, err := DataSQL(target.Engine, request.Schema, request.Table, structure.Columns, request)
	if err != nil {
		return nil, err
	}
	return execute(ctx, target, password, query, args, request.Limit)
}

func Query(ctx context.Context, target *domain.Target, password, query string) (*Result, error) {
	if err := ValidateSQL(query); err != nil {
		return nil, err
	}
	query = strings.TrimSuffix(strings.TrimSpace(query), ";")
	return execute(ctx, target, password, "SELECT * FROM ("+query+") AS dbb_readonly LIMIT 501", nil, 500)
}

func execute(ctx context.Context, target *domain.Target, password, query string, args []any, limit int) (*Result, error) {
	start := time.Now()
	out := &Result{Columns: []string{}, Rows: [][]any{}}
	err := readOnly(ctx, target, password, func(ctx context.Context, tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, query, args...)
		if err != nil {
			return err
		}
		defer rows.Close()
		out.Columns, err = rows.Columns()
		if err != nil {
			return err
		}
		size := 0
		for rows.Next() {
			if len(out.Rows) >= limit {
				out.Truncated = true
				break
			}
			row := make([]any, len(out.Columns))
			dest := make([]any, len(row))
			for i := range row {
				dest[i] = &row[i]
			}
			if err := rows.Scan(dest...); err != nil {
				return err
			}
			for i, v := range row {
				switch val := v.(type) {
				case []byte:
					row[i] = string(val)
				case int64:
					row[i] = fmt.Sprint(val)
				case uint64:
					row[i] = fmt.Sprint(val)
				case time.Time:
					row[i] = val.Format(time.RFC3339Nano)
				}
			}
			encoded, err := json.Marshal(row)
			if err != nil {
				return err
			}
			size += len(encoded)
			if size > 2<<20 {
				if len(out.Rows) == 0 {
					return fmt.Errorf("单行结果超过 2 MiB，请选择更少的列")
				}
				out.Truncated = true
				break
			}
			out.Rows = append(out.Rows, row)
		}
		return rows.Err()
	})
	out.DurationMS = time.Since(start).Milliseconds()
	return out, err
}
