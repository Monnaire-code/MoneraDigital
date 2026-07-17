package migrations

import (
	"fmt"
	"strings"
)

func qualifyCompanyFundIntegrationSQL(statement, schema string) string {
	quoted := `"` + strings.ReplaceAll(schema, `"`, `""`) + `"`
	qualified := strings.ReplaceAll(statement, "public.", quoted+".")
	qualified = strings.ReplaceAll(qualified, "pg_catalog, public", "pg_catalog, "+quoted)
	return qualified
}

func companyFundIntegrationSchemaName(prefix string, suffix int64) string {
	return fmt.Sprintf("%s_%d", prefix, suffix)
}
