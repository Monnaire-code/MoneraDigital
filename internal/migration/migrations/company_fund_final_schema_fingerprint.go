package migrations

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
)

const finalSafeheronOccurrenceIndexPredicate = "channel = 'SAFEHERON' AND provider_occurrence_key IS NOT NULL"
const finalSafeheronOccurrenceIndexName = "idx_company_fund_transactions_safeheron_occurrence"
const finalSafeheronRequiredConstraintDefinition = "CHECK (channel <> 'SAFEHERON' OR (provider_occurrence_key IS NOT NULL AND btrim(provider_occurrence_key) <> '' AND provider_occurrence_algorithm_version = 'safeheron-occurrence-v1'))"
const finalCompanyFundTransactionsTable = "company_fund_transactions"
const finalCompanyFundHistoryTable = "company_fund_transaction_valuation_history"
const finalManualProjectionFunctionName = "company_fund_enforce_manual_valuation_projection"
const finalManualProjectionTriggerType int64 = 19 // ROW | BEFORE | UPDATE

var finalManualConstraintNames = []string{
	"company_fund_transactions_usd_valuation_source_check",
	"company_fund_valuation_history_source_check",
	"company_fund_valuation_history_manual_metadata_check",
}

var finalConstraintTables = map[string]string{
	"company_fund_transactions_usd_valuation_source_check": finalCompanyFundTransactionsTable,
	"company_fund_valuation_history_source_check":          finalCompanyFundHistoryTable,
	"company_fund_valuation_history_manual_metadata_check": finalCompanyFundHistoryTable,
	migration053ConstraintName:                             finalCompanyFundTransactionsTable,
}

var finalManualConstraintDefinitions = mustExtractMigration052ConstraintDefinitions(migration052SchemaDDL)
var finalManualProjectionFunctionSource = mustExtractMigration052FunctionSource(migration052SchemaDDL)

type FinalCompanyFundSchemaSnapshot struct {
	OccurrenceSchema                      string            `json:"occurrence_schema"`
	OccurrenceTable                       string            `json:"occurrence_table"`
	OccurrenceColumns                     map[string]string `json:"occurrence_columns"`
	OccurrenceColumnLengths               map[string]int64  `json:"occurrence_column_lengths"`
	OccurrenceColumnsNullable             map[string]bool   `json:"occurrence_columns_nullable"`
	SafeheronOccurrenceIndexSchema        string            `json:"safeheron_occurrence_index_schema"`
	SafeheronOccurrenceIndexTable         string            `json:"safeheron_occurrence_index_table"`
	SafeheronOccurrenceIndexName          string            `json:"safeheron_occurrence_index_name"`
	SafeheronOccurrenceIndexUnique        bool              `json:"safeheron_occurrence_index_unique"`
	SafeheronOccurrenceIndexValid         bool              `json:"safeheron_occurrence_index_valid"`
	SafeheronOccurrenceIndexReady         bool              `json:"safeheron_occurrence_index_ready"`
	SafeheronOccurrenceIndexColumns       []string          `json:"safeheron_occurrence_index_columns"`
	SafeheronOccurrenceIndexPredicate     string            `json:"safeheron_occurrence_index_predicate"`
	SafeheronOccurrenceIndexDefinition    string            `json:"safeheron_occurrence_index_definition"`
	SafeheronRequiredConstraintName       string            `json:"safeheron_required_constraint_name"`
	SafeheronRequiredConstraintDefinition string            `json:"safeheron_required_constraint_definition"`
	SafeheronRequiredConstraintValidated  bool              `json:"safeheron_required_constraint_validated"`
	ManualConstraintNames                 []string          `json:"manual_constraint_names"`
	ConstraintSchemas                     map[string]string `json:"constraint_schemas"`
	ConstraintTables                      map[string]string `json:"constraint_tables"`
	ConstraintTypes                       map[string]string `json:"constraint_types"`
	ConstraintOccurrences                 map[string]int64  `json:"constraint_occurrences"`
	ManualConstraintDefinitions           map[string]string `json:"manual_constraint_definitions"`
	ManualConstraintsValidated            map[string]bool   `json:"manual_constraints_validated"`
	ManualProjectionFunctionSchema        string            `json:"manual_projection_function_schema"`
	ManualProjectionFunctionName          string            `json:"manual_projection_function_name"`
	ManualProjectionFunctionArgumentCount int64             `json:"manual_projection_function_argument_count"`
	ManualProjectionFunctionResult        string            `json:"manual_projection_function_result"`
	ManualProjectionFunctionLanguage      string            `json:"manual_projection_function_language"`
	ManualProjectionFunctionKind          string            `json:"manual_projection_function_kind"`
	ManualProjectionFunctionOID           int64             `json:"manual_projection_function_oid"`
	ManualProjectionFunctionSource        string            `json:"manual_projection_function_source"`
	ManualProjectionTriggerSchema         string            `json:"manual_projection_trigger_schema"`
	ManualProjectionTriggerTable          string            `json:"manual_projection_trigger_table"`
	ManualProjectionTriggerFunctionSchema string            `json:"manual_projection_trigger_function_schema"`
	ManualProjectionTriggerFunctionName   string            `json:"manual_projection_trigger_function_name"`
	ManualProjectionTriggerFunctionOID    int64             `json:"manual_projection_trigger_function_oid"`
	ManualProjectionTriggerInternal       bool              `json:"manual_projection_trigger_internal"`
	ManualProjectionTriggerEnabled        string            `json:"manual_projection_trigger_enabled"`
	ManualProjectionTriggerType           int64             `json:"manual_projection_trigger_type"`
	ManualProjectionTriggerColumns        []string          `json:"manual_projection_trigger_columns"`
}

type FinalCompanyFundSchemaFingerprint struct {
	CanonicalJSON json.RawMessage `json:"canonical_json"`
	SHA256        string          `json:"sha256"`
}

func BuildFinalCompanyFundSchemaFingerprint(snapshot FinalCompanyFundSchemaSnapshot) (FinalCompanyFundSchemaFingerprint, error) {
	normalized := normalizeFinalCompanyFundSchemaSnapshot(snapshot)
	if err := validateFinalCompanyFundSchema(normalized); err != nil {
		return FinalCompanyFundSchemaFingerprint{}, err
	}
	data, _ := json.Marshal(normalized)
	digest := sha256.Sum256(data)
	return FinalCompanyFundSchemaFingerprint{CanonicalJSON: json.RawMessage(data), SHA256: hex.EncodeToString(digest[:])}, nil
}

func normalizeFinalCompanyFundSchemaSnapshot(snapshot FinalCompanyFundSchemaSnapshot) FinalCompanyFundSchemaSnapshot {
	normalized := snapshot
	normalized.OccurrenceColumns = cloneFinalSchemaColumns(snapshot.OccurrenceColumns)
	normalized.ManualConstraintNames = append([]string(nil), snapshot.ManualConstraintNames...)
	normalized.SafeheronOccurrenceIndexColumns = append([]string(nil), snapshot.SafeheronOccurrenceIndexColumns...)
	normalized.ManualProjectionTriggerColumns = append([]string(nil), snapshot.ManualProjectionTriggerColumns...)
	normalized.ConstraintSchemas = cloneFinalSchemaColumns(snapshot.ConstraintSchemas)
	normalized.ConstraintTables = cloneFinalSchemaColumns(snapshot.ConstraintTables)
	normalized.ConstraintTypes = cloneFinalSchemaColumns(snapshot.ConstraintTypes)
	normalized.ConstraintOccurrences = cloneFinalSchemaInt64s(snapshot.ConstraintOccurrences)
	normalized.ManualConstraintDefinitions = cloneFinalSchemaColumns(snapshot.ManualConstraintDefinitions)
	normalized.ManualConstraintsValidated = cloneFinalSchemaBools(snapshot.ManualConstraintsValidated)
	sort.Strings(normalized.ManualConstraintNames)
	normalized.SafeheronOccurrenceIndexPredicate = normalizeFinalSchemaSQL(snapshot.SafeheronOccurrenceIndexPredicate)
	normalized.SafeheronOccurrenceIndexDefinition = normalizeFinalSchemaSQL(snapshot.SafeheronOccurrenceIndexDefinition)
	normalized.SafeheronRequiredConstraintDefinition = normalizeFinalSchemaSQL(snapshot.SafeheronRequiredConstraintDefinition)
	normalized.ManualProjectionFunctionSource = normalizeFinalSchemaSQL(snapshot.ManualProjectionFunctionSource)
	for name, definition := range normalized.ManualConstraintDefinitions {
		normalized.ManualConstraintDefinitions[name] = normalizeFinalSchemaSQL(definition)
	}
	return normalized
}

func validateFinalCompanyFundSchema(snapshot FinalCompanyFundSchemaSnapshot) error {
	if err := validateFinalCompanyFundSchemaA(snapshot); err != nil {
		return err
	}
	if err := validateExactConstraint(snapshot, migration053ConstraintName, snapshot.SafeheronRequiredConstraintDefinition, snapshot.SafeheronRequiredConstraintValidated, finalSafeheronRequiredConstraintDefinition); err != nil {
		return fmt.Errorf("final schema requires exact Migration B check: %w", err)
	}
	if snapshot.SafeheronRequiredConstraintName != migration053ConstraintName {
		return fmt.Errorf("final schema requires exact Migration B check name")
	}
	return nil
}

func validateFinalCompanyFundSchemaA(snapshot FinalCompanyFundSchemaSnapshot) error {
	if snapshot.OccurrenceSchema != "public" || snapshot.OccurrenceTable != finalCompanyFundTransactionsTable || len(snapshot.OccurrenceColumns) != 2 || snapshot.OccurrenceColumns["provider_occurrence_key"] != "character varying" || snapshot.OccurrenceColumns["provider_occurrence_algorithm_version"] != "character varying" || snapshot.OccurrenceColumnLengths["provider_occurrence_key"] != 256 || snapshot.OccurrenceColumnLengths["provider_occurrence_algorithm_version"] != 64 || !snapshot.OccurrenceColumnsNullable["provider_occurrence_key"] || !snapshot.OccurrenceColumnsNullable["provider_occurrence_algorithm_version"] {
		return fmt.Errorf("final schema requires exact Migration A occurrence columns")
	}
	if err := validateFinalOccurrenceIndex(snapshot); err != nil {
		return err
	}
	return validateFinalManualSchema(snapshot)
}

func validateFinalOccurrenceIndex(snapshot FinalCompanyFundSchemaSnapshot) error {
	expectedDefinition := fmt.Sprintf("CREATE UNIQUE INDEX %s ON %s.%s USING btree (provider_occurrence_key) WHERE (%s)", finalSafeheronOccurrenceIndexName, snapshot.OccurrenceSchema, finalCompanyFundTransactionsTable, finalSafeheronOccurrenceIndexPredicate)
	valid := snapshot.SafeheronOccurrenceIndexSchema == snapshot.OccurrenceSchema && snapshot.SafeheronOccurrenceIndexTable == finalCompanyFundTransactionsTable && snapshot.SafeheronOccurrenceIndexName == finalSafeheronOccurrenceIndexName
	valid = valid && snapshot.SafeheronOccurrenceIndexUnique && snapshot.SafeheronOccurrenceIndexValid && snapshot.SafeheronOccurrenceIndexReady && equalStringSlices(snapshot.SafeheronOccurrenceIndexColumns, []string{"provider_occurrence_key"})
	valid = valid && normalizeFinalCatalogExpression(snapshot.SafeheronOccurrenceIndexPredicate) == normalizeFinalCatalogExpression(finalSafeheronOccurrenceIndexPredicate)
	valid = valid && normalizeFinalIndexDefinition(snapshot.SafeheronOccurrenceIndexDefinition) == normalizeFinalIndexDefinition(expectedDefinition)
	if !valid {
		return fmt.Errorf("final schema requires the exact valid and ready Safeheron partial unique index")
	}
	return nil
}

func validateFinalManualSchema(snapshot FinalCompanyFundSchemaSnapshot) error {
	expectedNames := append([]string(nil), finalManualConstraintNames...)
	sort.Strings(expectedNames)
	if !equalStringSlices(snapshot.ManualConstraintNames, expectedNames) {
		return fmt.Errorf("final schema requires exact MANUAL constraints")
	}
	for _, name := range expectedNames {
		if err := validateExactConstraint(snapshot, name, snapshot.ManualConstraintDefinitions[name], snapshot.ManualConstraintsValidated[name], finalManualConstraintDefinitions[name]); err != nil {
			return fmt.Errorf("final schema MANUAL constraint %s: %w", name, err)
		}
	}
	if err := validateFinalManualFunction(snapshot); err != nil {
		return err
	}
	return validateFinalManualTrigger(snapshot)
}

func validateExactConstraint(snapshot FinalCompanyFundSchemaSnapshot, name, definition string, validated bool, expectedDefinition string) error {
	if snapshot.ConstraintOccurrences[name] != 1 || snapshot.ConstraintSchemas[name] != snapshot.OccurrenceSchema || snapshot.ConstraintTables[name] != finalConstraintTables[name] || snapshot.ConstraintTypes[name] != "c" || !validated {
		return fmt.Errorf("catalog ownership, type, or validation differs")
	}
	if normalizeFinalCatalogExpression(definition) != normalizeFinalCatalogExpression(expectedDefinition) {
		return fmt.Errorf("definition differs")
	}
	return nil
}

func validateFinalManualFunction(snapshot FinalCompanyFundSchemaSnapshot) error {
	valid := snapshot.ManualProjectionFunctionSchema == snapshot.OccurrenceSchema && snapshot.ManualProjectionFunctionName == finalManualProjectionFunctionName
	valid = valid && snapshot.ManualProjectionFunctionArgumentCount == 0 && snapshot.ManualProjectionFunctionResult == "trigger" && snapshot.ManualProjectionFunctionLanguage == "plpgsql" && snapshot.ManualProjectionFunctionKind == "f" && snapshot.ManualProjectionFunctionOID > 0
	valid = valid && normalizeFinalSchemaSQL(snapshot.ManualProjectionFunctionSource) == normalizeFinalSchemaSQL(finalManualProjectionFunctionSource)
	if !valid {
		return fmt.Errorf("final schema MANUAL function is not the exact zero-argument plpgsql trigger function")
	}
	return nil
}

func validateFinalManualTrigger(snapshot FinalCompanyFundSchemaSnapshot) error {
	valid := snapshot.ManualProjectionTriggerSchema == snapshot.OccurrenceSchema && snapshot.ManualProjectionTriggerTable == finalCompanyFundTransactionsTable
	valid = valid && snapshot.ManualProjectionTriggerFunctionSchema == snapshot.OccurrenceSchema && snapshot.ManualProjectionTriggerFunctionName == finalManualProjectionFunctionName && snapshot.ManualProjectionTriggerFunctionOID == snapshot.ManualProjectionFunctionOID
	valid = valid && !snapshot.ManualProjectionTriggerInternal && snapshot.ManualProjectionTriggerEnabled == "O" && snapshot.ManualProjectionTriggerType == finalManualProjectionTriggerType && equalStringSlices(snapshot.ManualProjectionTriggerColumns, migration052ProtectedProjectionColumns)
	if !valid {
		return fmt.Errorf("final schema MANUAL trigger ownership, function, mode, or protected columns differ")
	}
	return nil
}

func extractMigration052ConstraintDefinition(ddl, name string) (string, error) {
	pattern := `(?s)ADD CONSTRAINT\s+` + regexp.QuoteMeta(name) + `\s+(CHECK\s*\(.*?\))\s*;`
	match := regexp.MustCompile(pattern).FindStringSubmatch(ddl)
	if len(match) != 2 {
		return "", fmt.Errorf("constraint %s not found in migration 052 DDL", name)
	}
	return normalizeFinalSchemaSQL(match[1]), nil
}

func mustExtractMigration052ConstraintDefinitions(ddl string) map[string]string {
	definitions := make(map[string]string, len(finalManualConstraintNames))
	for _, name := range finalManualConstraintNames {
		definition, err := extractMigration052ConstraintDefinition(ddl, name)
		if err != nil {
			panic(err)
		}
		definitions[name] = definition
	}
	return definitions
}

func extractMigration052FunctionSource(ddl string) (string, error) {
	prefix := "CREATE OR REPLACE FUNCTION public." + finalManualProjectionFunctionName + "()"
	_, after, found := strings.Cut(ddl, prefix)
	if !found {
		return "", fmt.Errorf("MANUAL function declaration not found in migration 052 DDL")
	}
	_, source, found := strings.Cut(after, "AS $$")
	if !found {
		return "", fmt.Errorf("MANUAL function body start not found in migration 052 DDL")
	}
	source, _, found = strings.Cut(source, "$$;")
	if !found {
		return "", fmt.Errorf("MANUAL function body end not found in migration 052 DDL")
	}
	return strings.TrimSpace(source), nil
}

func mustExtractMigration052FunctionSource(ddl string) string {
	source, err := extractMigration052FunctionSource(ddl)
	if err != nil {
		panic(err)
	}
	return source
}

func cloneFinalSchemaColumns(columns map[string]string) map[string]string {
	cloned := make(map[string]string, len(columns))
	for name, value := range columns {
		cloned[name] = value
	}
	return cloned
}

func cloneFinalSchemaBools(values map[string]bool) map[string]bool {
	cloned := make(map[string]bool, len(values))
	for name, value := range values {
		cloned[name] = value
	}
	return cloned
}

func cloneFinalSchemaInt64s(values map[string]int64) map[string]int64 {
	cloned := make(map[string]int64, len(values))
	for name, value := range values {
		cloned[name] = value
	}
	return cloned
}

func normalizeFinalSchemaSQL(value string) string { return strings.Join(strings.Fields(value), " ") }

func normalizeFinalCatalogExpression(value string) string {
	value = normalizeFinalCatalogSyntax(value)
	if strings.HasPrefix(value, "CHECK ") {
		return "CHECK " + canonicalFinalBooleanExpression(strings.TrimSpace(strings.TrimPrefix(value, "CHECK ")))
	}
	return canonicalFinalBooleanExpression(value)
}

func normalizeFinalIndexDefinition(value string) string {
	value = normalizeFinalCatalogSyntax(value)
	prefix, predicate, found := strings.Cut(value, " WHERE ")
	if !found {
		return value
	}
	return prefix + " WHERE " + canonicalFinalBooleanExpression(predicate)
}

func normalizeFinalCatalogSyntax(value string) string {
	value = normalizeFinalSchemaSQL(value)
	typePattern := `(?i)(?:text|character varying|varchar|bpchar)(?:\[\])?`
	value = regexp.MustCompile(`\(([A-Za-z_][A-Za-z0-9_.]*)\)::`+typePattern).ReplaceAllString(value, "$1")
	value = regexp.MustCompile(`::`+typePattern).ReplaceAllString(value, "")
	value = regexp.MustCompile(`(?i)=\s*ANY\s*\(\s*ARRAY\s*\[(.*?)\]\s*\)`).ReplaceAllString(value, " IN ($1)")
	return normalizeFinalSchemaSQL(value)
}

func canonicalFinalBooleanExpression(value string) string {
	value = stripFinalOuterParentheses(strings.TrimSpace(value))
	if parts := splitFinalTopLevelBoolean(value, " OR "); len(parts) > 1 {
		return "OR(" + canonicalFinalBooleanParts(parts) + ")"
	}
	if parts := splitFinalTopLevelBoolean(value, " AND "); len(parts) > 1 {
		return "AND(" + canonicalFinalBooleanParts(parts) + ")"
	}
	return normalizeFinalSchemaSQL(value)
}

func canonicalFinalBooleanParts(parts []string) string {
	canonical := make([]string, len(parts))
	for index, part := range parts {
		canonical[index] = canonicalFinalBooleanExpression(part)
	}
	return strings.Join(canonical, ",")
}

func splitFinalTopLevelBoolean(value, separator string) []string {
	parts := make([]string, 0, 2)
	depth, start := 0, 0
	inQuote := false
	for index := 0; index < len(value); index++ {
		switch value[index] {
		case '\'':
			inQuote = !inQuote
		case '(':
			if !inQuote {
				depth++
			}
		case ')':
			if !inQuote {
				depth--
			}
		}
		if !inQuote && depth == 0 && strings.HasPrefix(value[index:], separator) {
			parts = append(parts, strings.TrimSpace(value[start:index]))
			index += len(separator) - 1
			start = index + 1
		}
	}
	return append(parts, strings.TrimSpace(value[start:]))
}

func stripFinalOuterParentheses(value string) string {
	for len(value) >= 2 && value[0] == '(' && value[len(value)-1] == ')' && finalOuterParenthesesEncloseExpression(value) {
		value = strings.TrimSpace(value[1 : len(value)-1])
	}
	return value
}

func finalOuterParenthesesEncloseExpression(value string) bool {
	depth := 0
	inQuote := false
	for index := 0; index < len(value); index++ {
		if value[index] == '\'' {
			inQuote = !inQuote
			continue
		}
		if inQuote {
			continue
		}
		if value[index] == '(' {
			depth++
		} else if value[index] == ')' {
			depth--
			if depth == 0 && index != len(value)-1 {
				return false
			}
		}
	}
	return depth == 0
}

func equalStringSlices(actual, expected []string) bool {
	if len(actual) != len(expected) {
		return false
	}
	for index := range expected {
		if actual[index] != expected[index] {
			return false
		}
	}
	return true
}
