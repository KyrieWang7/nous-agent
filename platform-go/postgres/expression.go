package postgres

import (
	"fmt"
	"strconv"
	"strings"

	"gorm.io/gorm/clause"
)

// queryExpression binds PostgreSQL $n parameters through GORM, preserving
// repeated parameters, quoted literals, JSON operators and dollar-quoted SQL.
// Values are always sent as driver parameters, never formatted into SQL.
type queryExpression struct {
	SQL  string
	Args []any
}

func (e queryExpression) Build(b clause.Builder) {
	q := e.SQL
	start := 0
	for i := 0; i < len(q); {
		if q[i] == '\'' || q[i] == '"' {
			quote := q[i]
			escapes := quote == '\'' && i > 0 && (q[i-1] == 'E' || q[i-1] == 'e')
			i++
			for i < len(q) {
				if q[i] == quote {
					if i+1 < len(q) && q[i+1] == quote {
						i += 2
						continue
					}
					i++
					break
				}
				if q[i] == '\\' && escapes && i+1 < len(q) {
					i += 2
				} else {
					i++
				}
			}
			continue
		}
		if strings.HasPrefix(q[i:], "--") {
			if end := strings.IndexByte(q[i:], '\n'); end >= 0 {
				i += end + 1
			} else {
				i = len(q)
			}
			continue
		}
		if strings.HasPrefix(q[i:], "/*") {
			depth := 1
			i += 2
			for i < len(q) && depth > 0 {
				if strings.HasPrefix(q[i:], "/*") {
					depth++
					i += 2
				} else if strings.HasPrefix(q[i:], "*/") {
					depth--
					i += 2
				} else {
					i++
				}
			}
			continue
		}
		if q[i] != '$' {
			i++
			continue
		}
		end := i + 1
		for end < len(q) && q[end] >= '0' && q[end] <= '9' {
			end++
		}
		if end > i+1 {
			index, _ := strconv.Atoi(q[i+1 : end])
			if index < 1 || index > len(e.Args) {
				_ = b.AddError(fmt.Errorf("postgres: missing parameter $%d", index))
				return
			}
			_, _ = b.WriteString(q[start:i])
			b.AddVar(b, e.Args[index-1])
			i = end
			start = i
			continue
		}
		// A PostgreSQL dollar quote is $$ or $identifier$.
		end = i + 1
		for end < len(q) && ((q[end] >= 'a' && q[end] <= 'z') || (q[end] >= 'A' && q[end] <= 'Z') || q[end] == '_' || (end > i+1 && q[end] >= '0' && q[end] <= '9')) {
			end++
		}
		if end < len(q) && q[end] == '$' {
			delimiter := q[i : end+1]
			if close := strings.Index(q[end+1:], delimiter); close >= 0 {
				i = end + 1 + close + len(delimiter)
				continue
			}
		}
		i++
	}
	_, _ = b.WriteString(q[start:])
}
