package main

import (
	"strings"
	"unicode"
)

var initialisms = map[string]string{
	"api": "API",
	"cpu": "CPU",
	"hw":  "HW",
	"id":  "ID",
	"io":  "IO",
	"ipc": "IPC",
	"pid": "PID",
	"rss": "RSS",
	"wal": "WAL",
}

func ident(s string) string {
	var b strings.Builder
	for _, part := range strings.FieldsFunc(s, func(r rune) bool {
		return r == '.' || r == '_' || r == '-'
	}) {
		if up, ok := initialisms[part]; ok {
			b.WriteString(up)
			continue
		}
		runes := []rune(part)
		if len(runes) == 0 {
			continue
		}
		b.WriteRune(unicode.ToUpper(runes[0]))
		b.WriteString(string(runes[1:]))
	}
	return b.String()
}
