package model

import "gorm.io/gorm"

// filterUsersByExclusiveRatio checks for stored configuration, not effective
// billing. Legacy null/negative entries still count as configured. Arrays and
// malformed/non-numeric maps do not. Keep the list badge's predicate in sync.
func filterUsersByExclusiveRatio(query *gorm.DB, present bool) *gorm.DB {
	if query.Dialector.Name() == "sqlite" {
		// CASE guards every JSON operation from malformed historical text.
		return query.Where(`(CASE WHEN json_valid(group_ratios) THEN
			json_type(group_ratios) = 'object'
			AND EXISTS (SELECT 1 FROM json_each(group_ratios))
			AND NOT EXISTS (SELECT 1 FROM json_each(group_ratios) WHERE type NOT IN ('integer', 'real', 'null'))
			ELSE 0 END) = ?`, present)
	}

	// Flat number/null JSON grammar avoids throwing casts on PostgreSQL 9.6.
	// Passing the pattern as a parameter also avoids SQL string escaping rules.
	const whitespace = "[ \t\r\n]*"
	const key = `"([^"\\` + "\x01-\x1f" + `]|\\(["\\/bfnrt]|u[0-9a-fA-F]{4}))*"`
	const number = `-?(0|[1-9][0-9]*)(\.[0-9]+)?([eE][+-]?[0-9]+)?`
	const member = key + whitespace + `:` + whitespace + `(` + number + `|null)`
	const pattern = `^` + whitespace + `\{` + whitespace + member + `(` + whitespace + `,` + whitespace + member + `)*` + whitespace + `\}` + whitespace + `$`
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
