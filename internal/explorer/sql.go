package explorer

import (
	"fmt"
	"strings"
	"unicode"
)

type token struct {
	text string
	kind byte
}

func tokens(query string) ([]token, error) {
	var out []token
	for i := 0; i < len(query); {
		c := query[i]
		if c == ' ' || c == '\r' || c == '\n' || c == '\t' {
			i++
			continue
		}
		if c == '\'' || c == '"' || c == '`' {
			quote := c
			start := i
			i++
			closed := false
			for i < len(query) {
				if query[i] == '\\' {
					return nil, fmt.Errorf("不支持反斜杠转义，请使用标准 SQL 引号转义")
				}
				if query[i] == quote {
					i++
					if i < len(query) && query[i] == quote {
						i++
						continue
					}
					closed = true
					break
				}
				i++
			}
			if !closed {
				return nil, fmt.Errorf("引号未闭合")
			}
			out = append(out, token{query[start:i], quote})
			continue
		}
		if (c == '-' && i+1 < len(query) && query[i+1] == '-') || (c == '/' && i+1 < len(query) && query[i+1] == '*') || strings.ContainsRune("#@$:\\", rune(c)) {
			return nil, fmt.Errorf("不支持注释、变量、类型转换或特殊 SQL 语法")
		}
		if c == '_' || c > 127 || unicode.IsLetter(rune(c)) {
			start := i
			i++
			for i < len(query) && (query[i] == '_' || query[i] > 127 || unicode.IsLetter(rune(query[i])) || unicode.IsDigit(rune(query[i]))) {
				i++
			}
			out = append(out, token{strings.ToLower(query[start:i]), 'w'})
			continue
		}
		if !strings.ContainsRune("0123456789.,()*+-/%=<>!;", rune(c)) {
			return nil, fmt.Errorf("不支持的 SQL 字符")
		}
		out = append(out, token{string(c), c})
		i++
	}
	return out, nil
}

func ValidateSQL(query string) error {
	if len(query) > 32768 {
		return fmt.Errorf("SQL 不能超过 32 KiB")
	}
	ts, err := tokens(query)
	if err != nil {
		return err
	}
	if len(ts) == 0 || ts[0].text != "select" {
		return fmt.Errorf("仅支持单条 SELECT 查询（不支持 CTE）")
	}
	denied := " insert update delete create alter drop truncate grant revoke copy call execute do into outfile dumpfile for lock unlock set reset use load handler procedure function returning with recursive operator collate cast convert information_schema pg_catalog mysql performance_schema sys "
	functions := " count sum avg min max abs round ceil floor lower upper length char_length coalesce nullif concat substring trim "
	for i, t := range ts {
		if t.text == ";" && i != len(ts)-1 {
			return fmt.Errorf("只允许执行一条 SQL")
		}
		if (t.kind == '"' || t.kind == '`') && i+1 < len(ts) && ts[i+1].text == "." && strings.Contains(" information_schema pg_catalog mysql performance_schema sys ", " "+strings.ToLower(t.text[1:len(t.text)-1])+" ") {
			return fmt.Errorf("不支持直接查询系统 schema")
		}
		if t.kind == 'w' && strings.Contains(denied, " "+t.text+" ") {
			return fmt.Errorf("不允许的 SQL 关键字：%s", t.text)
		}
		if i+1 < len(ts) && ts[i+1].text == "(" && (t.kind == 'w' || t.kind == '"' || t.kind == '`') {
			if t.kind == 'w' && strings.Contains(" select distinct from where and or not in exists on having when then else ", " "+t.text+" ") && (i == 0 || ts[i-1].text != ".") {
				continue
			}
			if t.kind != 'w' || !strings.Contains(functions, " "+t.text+" ") || (i > 0 && ts[i-1].text == ".") {
				return fmt.Errorf("仅支持内置只读聚合与字符串函数，不允许调用自定义函数")
			}
		}
	}
	return nil
}

func Quote(engine, name string) string {
	q := "\""
	if engine != "postgres" {
		q = "`"
	}
	return q + strings.ReplaceAll(name, q, q+q) + q
}

type DataRequest struct {
	Schema   string `json:"schema"`
	Table    string `json:"table"`
	Column   string `json:"column"`
	Operator string `json:"operator"`
	Value    string `json:"value"`
	Limit    int    `json:"limit"`
	Offset   int    `json:"offset"`
}

func DataSQL(engine, schema, table string, columns []Column, r DataRequest) (string, []any, error) {
	if r.Limit < 1 || r.Limit > 500 || r.Offset < 0 || r.Offset > 1000000 {
		return "", nil, fmt.Errorf("每页 1~500 行，偏移量不超过 1000000")
	}
	query := "SELECT * FROM " + Quote(engine, schema) + "." + Quote(engine, table)
	args := []any{}
	if r.Column != "" {
		known := false
		for _, c := range columns {
			if c.Name == r.Column {
				known = true
			}
		}
		if !known {
			return "", nil, fmt.Errorf("筛选列不存在")
		}
		op, ok := map[string]string{"eq": "=", "ne": "<>", "gt": ">", "gte": ">=", "lt": "<", "lte": "<=", "like": "LIKE", "null": "IS NULL", "notnull": "IS NOT NULL"}[r.Operator]
		if !ok {
			return "", nil, fmt.Errorf("不支持的筛选运算符")
		}
		query += " WHERE " + Quote(engine, r.Column) + " " + op
		if r.Operator != "null" && r.Operator != "notnull" {
			placeholder := "?"
			if engine == "postgres" {
				placeholder = "$1"
			}
			query += " " + placeholder
			args = append(args, r.Value)
		}
	}
	var primary []string
	for _, c := range columns {
		if c.Primary {
			primary = append(primary, Quote(engine, c.Name))
		}
	}
	if len(primary) > 0 {
		query += " ORDER BY " + strings.Join(primary, ",")
	}
	query += fmt.Sprintf(" LIMIT %d OFFSET %d", r.Limit+1, r.Offset)
	return query, args, nil
}
