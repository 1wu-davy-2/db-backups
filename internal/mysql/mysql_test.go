package mysql

import (
	"strings"
	"testing"

	"github.com/weigong/dbbackups/internal/domain"
)

func TestParseVersion(t *testing.T) {
	for _, tc := range []struct{ input, engine, version string }{
		{"mysqldump Ver 8.0.44 for Win64 on x86_64 (MySQL Community Server - GPL)", "mysql", "8.0.44"},
		{"mysqldump Ver 10.19 Distrib 10.11.14-MariaDB, for Win64", "mariadb", "10.11.14"},
		{"mariadb-dump from 11.4.8-MariaDB, client 10.19", "mariadb", "11.4.8"},
		{"5.5.5-10.6.24-MariaDB", "mariadb", "10.6.24"},
	} {
		got, err := ParseVersion(tc.input)
		if err != nil || got.Engine != tc.engine || got.Version != tc.version {
			t.Fatalf("%q => %+v, %v", tc.input, got, err)
		}
	}
	if _, err := ParseVersion("unknown"); err == nil {
		t.Fatal("accepted unknown version")
	}
}

func TestPickNeverCrossesVendorOrSeries(t *testing.T) {
	tc := &Toolchain{tools: []Tool{{Engine: "mysql", Version: "8.0.44", Major: 8, Minor: 0, Path: "mysql8"}, {Engine: "mariadb", Version: "10.11.14", Major: 10, Minor: 11, Path: "maria10"}}}
	got, err := tc.Pick("mariadb", "10.11.12-MariaDB")
	if err != nil || got.Path != "maria10" {
		t.Fatalf("%+v %v", got, err)
	}
	if _, err := tc.Pick("mysql", "8.4.5"); err == nil {
		t.Fatal("accepted mismatching series")
	}
	if _, err := tc.Pick("mysql", "10.11.14-MariaDB"); err == nil {
		t.Fatal("accepted wrong vendor")
	}
}

func TestClientConfigEscapesCredentials(t *testing.T) {
	target := &domain.Target{Host: "localhost", Port: 3306, Username: "backup", SSLMode: "disable"}
	text, err := ClientConfig(target, "a\"b\\c\n[client]\nuser=root")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(text, "[client]") != 2 || strings.Contains(text, "\nuser=root") || !strings.Contains(text, `password="a\"b\\c\n[client]\nuser=root"`) {
		t.Fatalf("unsafe config %q", text)
	}
	target.SSLMode = "invalid"
	if _, err := ClientConfig(target, "test"); err == nil {
		t.Fatal("accepted invalid TLS")
	}
}

func TestDumpArgumentsAndCompression(t *testing.T) {
	target := &domain.Target{DBName: "orders"}
	my := DumpArgs(Tool{Engine: "mysql", Major: 8}, target, "credentials.cnf")
	ma := DumpArgs(Tool{Engine: "mariadb", Major: 10}, target, "credentials.cnf")
	if my[0] != "--defaults-file=credentials.cnf" {
		t.Fatal(my)
	}
	if !strings.Contains(strings.Join(my, " "), "--column-statistics=0") || strings.Contains(strings.Join(ma, " "), "--set-gtid-purged") {
		t.Fatal(my, ma)
	}
	if my[len(my)-2] != "--" || my[len(my)-1] != "orders" {
		t.Fatal(my)
	}
	if _, err := CompressionLevel("zstd:5"); err == nil {
		t.Fatal("accepted PG compression")
	}
	if level, err := CompressionLevel("gzip:6"); err != nil || level != 6 {
		t.Fatal(level, err)
	}
}
