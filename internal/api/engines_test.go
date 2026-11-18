package api

import "testing"

func TestEngineDefaults(t *testing.T) {
	for _, engine := range []string{"postgres", "mysql", "mariadb"} {
		req := targetReq{Engine: engine, Name: "test", Host: "localhost", DBName: "app", Username: "backup"}
		if err := req.normalize(); err != nil {
			t.Fatal(err)
		}
		port := 3306
		if engine == "postgres" {
			port = 5432
		}
		if req.Port != port {
			t.Fatalf("%s: %d", engine, req.Port)
		}
		p := policyReq{CronExpr: "0 2 * * *", RetainCount: 7}
		if err := p.normalize(engine); err != nil {
			t.Fatal(err)
		}
		if engine != "postgres" && (p.DumpFormat != "sql" || p.Compression != "gzip:6" || p.VerifyMode != "checksum") {
			t.Fatalf("%+v", p)
		}
	}
}

func TestSQLPolicyRejectsPostgresOptions(t *testing.T) {
	for _, p := range []policyReq{
		{DumpFormat: "custom"}, {Compression: "zstd:5"}, {VerifyMode: "toc"},
	} {
		p.CronExpr = "0 2 * * *"
		p.RetainCount = 7
		if err := p.normalize("mariadb"); err == nil {
			t.Fatalf("accepted %+v", p)
		}
	}
}
