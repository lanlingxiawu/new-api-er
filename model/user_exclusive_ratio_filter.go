package model

import (
	"fmt"
	"regexp"
	"strings"
	"unicode/utf16"

	"gorm.io/gorm"
)

// filterUsersByExclusiveRatio checks for stored configuration, not effective
// billing. Legacy null/negative entries still count as configured. Arrays and
// malformed/non-numeric maps do not. Keep the list badge's predicate in sync.
func filterUsersByExclusiveRatio(query *gorm.DB, present bool, group string) *gorm.DB {
	if group != "" && !present {
		return query.Where("1 = 0")
	}
	if query.Dialector.Name() == "sqlite" {
		memberCondition := ""
		args := make([]any, 0, 2)
		if group != "" {
			memberCondition = " WHERE key = ? COLLATE BINARY"
			args = append(args, group)
		}
		args = append(args, present)
		// CASE guards every JSON operation from malformed historical text.
		return query.Where(`(CASE WHEN json_valid(group_ratios) THEN
			json_type(group_ratios) = 'object'
			AND EXISTS (SELECT 1 FROM json_each(group_ratios)`+memberCondition+`)
			AND NOT EXISTS (SELECT 1 FROM json_each(group_ratios) WHERE type NOT IN ('integer', 'real', 'null'))
			ELSE 0 END) = ?`, args...)
	}

	// Flat number/null JSON grammar avoids throwing casts on PostgreSQL 9.6.
	// Passing the pattern as a parameter also avoids SQL string escaping rules.
	const whitespace = "[ \t\r\n]*"
	const key = `"([^"\\` + "\x01-\x1f" + `]|\\(["\\/bfnrt]|u[0-9a-fA-F]{4}))*"`
	const number = `-?(0|[1-9][0-9]*)(\.[0-9]+)?([eE][+-]?[0-9]+)?`
	const member = key + whitespace + `:` + whitespace + `(` + number + `|null)`
	members := member + `(` + whitespace + `,` + whitespace + member + `)*`
	if group != "" {
		// Anchor to complete members, not a substring inside another key.
		target := exclusiveRatioGroupKeyPattern(group) + whitespace + `:` + whitespace + `(` + number + `|null)`
		members = `(` + member + whitespace + `,` + whitespace + `)*` + target + `(` + whitespace + `,` + whitespace + member + `)*`
	}
	pattern := `^` + whitespace + `\{` + whitespace + members + whitespace + `\}` + whitespace + `$`
	operator := "REGEXP"
	if query.Dialector.Name() == "postgres" {
		operator = "~"
	}
	column := "group_ratios"
	if query.Dialector.Name() == "mysql" {
		// The default case-insensitive collation would accept invalid NULL.
		column = "CONVERT(group_ratios USING utf8mb4) COLLATE utf8mb4_bin"
	}
	// Most users have no override. Avoid invoking the regexp engine for those
	// rows, especially on older PostgreSQL versions.
	return query.Where("(CASE WHEN group_ratios IS NULL OR group_ratios IN ('', '{}', 'null') THEN false ELSE COALESCE("+column+" "+operator+" ?, false) END) = ?", pattern, present)
}

// exclusiveRatioGroupKeyPattern matches a JSON key in both literal and escaped
// forms without casting historical TEXT to JSON. Use only common POSIX syntax
// for MySQL 5.7 and PostgreSQL 9.6, and quote every user-controlled character.
func exclusiveRatioGroupKeyPattern(group string) string {
	var pattern strings.Builder
	pattern.WriteByte('"')
	for _, character := range group {
		pattern.WriteByte('(')
		// Every code point has a Unicode escape form. Non-BMP characters use
		// a UTF-16 surrogate pair; hex letters may have either case.
		units := []rune{character}
		if character > 0xffff {
			high, low := utf16.EncodeRune(character)
			units = []rune{high, low}
		}
		for _, unit := range units {
			pattern.WriteString(`\\u`)
			for _, digit := range fmt.Sprintf("%04x", unit) {
				if digit >= 'a' && digit <= 'f' {
					pattern.WriteByte('[')
					pattern.WriteRune(digit)
					pattern.WriteRune(digit - 'a' + 'A')
					pattern.WriteByte(']')
				} else {
					pattern.WriteRune(digit)
				}
			}
		}
		var escaped string
		switch character {
		case '"':
			escaped = `\"`
		case '\\':
			escaped = `\\`
		case '/':
			escaped = `\/`
		case '\b':
			escaped = `\b`
		case '\f':
			escaped = `\f`
		case '\n':
			escaped = `\n`
		case '\r':
			escaped = `\r`
		case '\t':
			escaped = `\t`
		}
		if escaped != "" {
			pattern.WriteByte('|')
			pattern.WriteString(regexp.QuoteMeta(escaped))
		}
		if character >= 0x20 && character != '"' && character != '\\' {
			pattern.WriteByte('|')
			pattern.WriteString(regexp.QuoteMeta(string(character)))
		}
		pattern.WriteByte(')')
	}
	pattern.WriteByte('"')
	return pattern.String()
}
