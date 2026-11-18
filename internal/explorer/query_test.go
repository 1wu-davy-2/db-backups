package explorer

import "testing"

func TestReadOnlySQL(t *testing.T) {
	for _, q := range []string{"SELECT 1", "select id, name from public.orders where id > 1 order by id limit 20;", "SELECT count(*) FROM orders", "SELECT 'delete; --' AS label", `SELECT "id" FROM "orders"`, "SELECT * FROM orders WHERE id IN (1, 2)", "SELECT (1 + 2) AS n", "SELECT * FROM orders WHERE (id = 1 OR id = 2) AND (id > 0)", "SELECT * FROM (SELECT id FROM orders) AS o", "SELECT CASE WHEN (id = 1) THEN (2) ELSE (3) END FROM orders"} {
		if err := ValidateSQL(q); err != nil {
			t.Errorf("rejected %q: %v", q, err)
		}
	}
	for _, q := range []string{"", "DELETE FROM orders", "SELECT 1; DROP TABLE orders", "SELECT 1 /* ! */", "SELECT * INTO other FROM orders", "SELECT * FROM orders FOR UPDATE", "SELECT * FROM orders LOCK IN SHARE MODE", "WITH x AS (DELETE FROM orders RETURNING *) SELECT * FROM x", "SELECT pg_sleep(10)", "SELECT load_file('/etc/passwd')", "SELECT my_function()", "SELECT public.count(*) FROM orders", "SELECT `sleep`(10)", "SELECT @x := 1", "SELECT 1 INTO OUTFILE '/tmp/a'", "SELECT lo_export(1, '/tmp/a')", "SELECT 1::custom_type", "SELECT * FROM orders --comment", "SELECT * FROM orders #comment", "SELECT E'\\'; select 1", "SELECT * FROM information_schema.tables", `SELECT * FROM "pg_catalog".pg_authid`, "SELECT * FROM `mysql`.user"} {
		if err := ValidateSQL(q); err == nil {
			t.Errorf("accepted unsafe %q", q)
		}
	}
}

func TestIdentifiersAndFilters(t *testing.T) {
	if Quote("postgres", `a"b`) != `"a""b"` || Quote("mysql", "a`b") != "`a``b`" {
		t.Fatal("identifier escaping")
	}
	cols := []Column{{Name: "id"}, {Name: "name"}}
	q, args, err := DataSQL("postgres", "public", "orders", cols, DataRequest{Column: "name", Operator: "eq", Value: "';drop table orders;--", Limit: 20})
	if err != nil || len(args) != 1 || args[0] != "';drop table orders;--" || q != `SELECT * FROM "public"."orders" WHERE "name" = $1 LIMIT 21 OFFSET 0` {
		t.Fatal(q, args, err)
	}
	if _, _, err := DataSQL("mysql", "app", "orders", cols, DataRequest{Column: "missing", Operator: "eq", Limit: 20}); err == nil {
		t.Fatal("accepted unknown column")
	}
	if _, _, err := DataSQL("mysql", "app", "orders", cols, DataRequest{Limit: 10000}); err == nil {
		t.Fatal("accepted unbounded page")
	}
}
