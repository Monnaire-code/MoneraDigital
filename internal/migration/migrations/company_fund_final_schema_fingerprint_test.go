package migrations

import (
	"strings"
	"testing"
)

func TestBuildFinalCompanyFundSchemaFingerprintRejectsSchemaAAndAcceptsSchemaB(t *testing.T) {
	schemaA := validFinalCompanyFundSchemaSnapshot()
	schemaA.SafeheronRequiredConstraintDefinition = ""
	schemaA.SafeheronRequiredConstraintValidated = false
	if _, err := BuildFinalCompanyFundSchemaFingerprint(schemaA); err == nil {
		t.Fatal("Schema A without Migration B required check was accepted")
	}

	schemaB := validFinalCompanyFundSchemaSnapshot()
	fingerprint, err := BuildFinalCompanyFundSchemaFingerprint(schemaB)
	if err != nil {
		t.Fatal(err)
	}
	if len(fingerprint.SHA256) != 64 || len(fingerprint.CanonicalJSON) == 0 {
		t.Fatalf("fingerprint = %#v", fingerprint)
	}
	reordered := schemaB
	reordered.ManualConstraintNames = []string{
		"company_fund_valuation_history_manual_metadata_check",
		"company_fund_transactions_usd_valuation_source_check",
		"company_fund_valuation_history_source_check",
	}
	reorderedFingerprint, err := BuildFinalCompanyFundSchemaFingerprint(reordered)
	if err != nil || reorderedFingerprint.SHA256 != fingerprint.SHA256 {
		t.Fatalf("constraint order changed fingerprint: %#v, %v", reorderedFingerprint, err)
	}
}

func TestBuildFinalCompanyFundSchemaFingerprintRejectsInternallyConsistentShadowSchema(t *testing.T) {
	snapshot := validFinalCompanyFundSchemaSnapshot()
	snapshot.OccurrenceSchema = "shadow"
	snapshot.SafeheronOccurrenceIndexSchema = "shadow"
	snapshot.SafeheronOccurrenceIndexDefinition = strings.Replace(snapshot.SafeheronOccurrenceIndexDefinition, "public.", "shadow.", 1)
	for name := range snapshot.ConstraintSchemas {
		snapshot.ConstraintSchemas[name] = "shadow"
	}
	snapshot.ManualProjectionFunctionSchema = "shadow"
	snapshot.ManualProjectionTriggerSchema = "shadow"
	snapshot.ManualProjectionTriggerFunctionSchema = "shadow"
	if _, err := BuildFinalCompanyFundSchemaFingerprint(snapshot); err == nil {
		t.Fatal("internally consistent non-public company-fund schema was accepted")
	}
}

func TestBuildFinalCompanyFundSchemaFingerprintRejectsEveryMissingInvariant(t *testing.T) {
	for _, testCase := range []struct {
		name   string
		mutate func(*FinalCompanyFundSchemaSnapshot)
	}{
		{name: "occurrence key column", mutate: func(value *FinalCompanyFundSchemaSnapshot) {
			delete(value.OccurrenceColumns, "provider_occurrence_key")
		}},
		{name: "occurrence version column", mutate: func(value *FinalCompanyFundSchemaSnapshot) {
			value.OccurrenceColumns["provider_occurrence_algorithm_version"] = "text"
		}},
		{name: "partial index uniqueness", mutate: func(value *FinalCompanyFundSchemaSnapshot) { value.SafeheronOccurrenceIndexUnique = false }},
		{name: "partial index invalid", mutate: func(value *FinalCompanyFundSchemaSnapshot) { value.SafeheronOccurrenceIndexValid = false }},
		{name: "partial index not ready", mutate: func(value *FinalCompanyFundSchemaSnapshot) { value.SafeheronOccurrenceIndexReady = false }},
		{name: "partial index wrong schema", mutate: func(value *FinalCompanyFundSchemaSnapshot) { value.SafeheronOccurrenceIndexSchema = "wrong" }},
		{name: "partial index wrong table", mutate: func(value *FinalCompanyFundSchemaSnapshot) { value.SafeheronOccurrenceIndexTable = "wrong" }},
		{name: "partial index extra key", mutate: func(value *FinalCompanyFundSchemaSnapshot) {
			value.SafeheronOccurrenceIndexColumns = append(value.SafeheronOccurrenceIndexColumns, "channel")
		}},
		{name: "partial index predicate", mutate: func(value *FinalCompanyFundSchemaSnapshot) {
			value.SafeheronOccurrenceIndexPredicate = "channel = 'AIRWALLEX'"
		}},
		{name: "partial index definition", mutate: func(value *FinalCompanyFundSchemaSnapshot) {
			value.SafeheronOccurrenceIndexDefinition += " INCLUDE (channel)"
		}},
		{name: "required check definition", mutate: func(value *FinalCompanyFundSchemaSnapshot) { value.SafeheronRequiredConstraintDefinition += " OR TRUE" }},
		{name: "required check validation", mutate: func(value *FinalCompanyFundSchemaSnapshot) { value.SafeheronRequiredConstraintValidated = false }},
		{name: "required check name", mutate: func(value *FinalCompanyFundSchemaSnapshot) { value.SafeheronRequiredConstraintName = "wrong" }},
		{name: "required check wrong table", mutate: func(value *FinalCompanyFundSchemaSnapshot) {
			value.ConstraintTables[migration053ConstraintName] = finalCompanyFundHistoryTable
		}},
		{name: "required check duplicate", mutate: func(value *FinalCompanyFundSchemaSnapshot) {
			value.ConstraintOccurrences[migration053ConstraintName] = 2
		}},
		{name: "manual constraint", mutate: func(value *FinalCompanyFundSchemaSnapshot) {
			value.ManualConstraintNames = value.ManualConstraintNames[:2]
		}},
		{name: "wrong manual constraint", mutate: func(value *FinalCompanyFundSchemaSnapshot) {
			value.ManualConstraintNames[0] = "wrong_constraint"
		}},
		{name: "manual constraint definitions", mutate: func(value *FinalCompanyFundSchemaSnapshot) {
			delete(value.ManualConstraintDefinitions, "company_fund_valuation_history_source_check")
		}},
		{name: "manual constraint validation", mutate: func(value *FinalCompanyFundSchemaSnapshot) {
			value.ManualConstraintsValidated["company_fund_valuation_history_source_check"] = false
		}},
		{name: "manual constraint syntax", mutate: func(value *FinalCompanyFundSchemaSnapshot) {
			value.ManualConstraintDefinitions["company_fund_valuation_history_source_check"] = "NOT A CHECK"
		}},
		{name: "manual constraint extra literal", mutate: func(value *FinalCompanyFundSchemaSnapshot) {
			value.ManualConstraintDefinitions["company_fund_valuation_history_source_check"] = "CHECK (TRUE OR " + value.ManualConstraintDefinitions["company_fund_valuation_history_source_check"] + ")"
		}},
		{name: "manual constraint wrong literal", mutate: func(value *FinalCompanyFundSchemaSnapshot) {
			value.ManualConstraintDefinitions["company_fund_valuation_history_source_check"] = strings.Replace(value.ManualConstraintDefinitions["company_fund_valuation_history_source_check"], "'USD_PAR'", "'WRONG'", 1)
		}},
		{name: "manual constraint wrong schema", mutate: func(value *FinalCompanyFundSchemaSnapshot) {
			value.ConstraintSchemas[finalManualConstraintNames[0]] = "wrong"
		}},
		{name: "manual constraint wrong table", mutate: func(value *FinalCompanyFundSchemaSnapshot) {
			value.ConstraintTables[finalManualConstraintNames[0]] = finalCompanyFundHistoryTable
		}},
		{name: "manual constraint wrong type", mutate: func(value *FinalCompanyFundSchemaSnapshot) {
			value.ConstraintTypes[finalManualConstraintNames[0]] = "u"
		}},
		{name: "manual constraint duplicate", mutate: func(value *FinalCompanyFundSchemaSnapshot) {
			value.ConstraintOccurrences[finalManualConstraintNames[0]] = 2
		}},
		{name: "manual function schema", mutate: func(value *FinalCompanyFundSchemaSnapshot) { value.ManualProjectionFunctionSchema = "wrong" }},
		{name: "manual function name", mutate: func(value *FinalCompanyFundSchemaSnapshot) { value.ManualProjectionFunctionName = "wrong" }},
		{name: "manual function args", mutate: func(value *FinalCompanyFundSchemaSnapshot) { value.ManualProjectionFunctionArgumentCount = 1 }},
		{name: "manual function result", mutate: func(value *FinalCompanyFundSchemaSnapshot) { value.ManualProjectionFunctionResult = "boolean" }},
		{name: "manual function language", mutate: func(value *FinalCompanyFundSchemaSnapshot) { value.ManualProjectionFunctionLanguage = "sql" }},
		{name: "manual function kind", mutate: func(value *FinalCompanyFundSchemaSnapshot) { value.ManualProjectionFunctionKind = "p" }},
		{name: "manual function source", mutate: func(value *FinalCompanyFundSchemaSnapshot) { value.ManualProjectionFunctionSource += " PERFORM 1;" }},
		{name: "manual trigger table", mutate: func(value *FinalCompanyFundSchemaSnapshot) { value.ManualProjectionTriggerTable = "wrong" }},
		{name: "manual trigger function oid", mutate: func(value *FinalCompanyFundSchemaSnapshot) { value.ManualProjectionTriggerFunctionOID++ }},
		{name: "manual trigger internal", mutate: func(value *FinalCompanyFundSchemaSnapshot) { value.ManualProjectionTriggerInternal = true }},
		{name: "manual trigger disabled", mutate: func(value *FinalCompanyFundSchemaSnapshot) { value.ManualProjectionTriggerEnabled = "D" }},
		{name: "manual trigger timing", mutate: func(value *FinalCompanyFundSchemaSnapshot) { value.ManualProjectionTriggerType = 17 }},
		{name: "manual trigger protected column", mutate: func(value *FinalCompanyFundSchemaSnapshot) {
			value.ManualProjectionTriggerColumns = value.ManualProjectionTriggerColumns[:len(value.ManualProjectionTriggerColumns)-1]
		}},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			candidate := validFinalCompanyFundSchemaSnapshot()
			candidate.OccurrenceColumns = cloneFinalSchemaColumns(candidate.OccurrenceColumns)
			candidate.ManualConstraintNames = append([]string(nil), candidate.ManualConstraintNames...)
			testCase.mutate(&candidate)
			if _, err := BuildFinalCompanyFundSchemaFingerprint(candidate); err == nil {
				t.Fatal("invalid final schema accepted")
			}
		})
	}
}

func validFinalCompanyFundSchemaSnapshot() FinalCompanyFundSchemaSnapshot {
	schema := "public"
	constraintSchemas := make(map[string]string, len(finalConstraintTables))
	constraintTables := make(map[string]string, len(finalConstraintTables))
	constraintTypes := make(map[string]string, len(finalConstraintTables))
	constraintOccurrences := make(map[string]int64, len(finalConstraintTables))
	for name, table := range finalConstraintTables {
		constraintSchemas[name], constraintTables[name], constraintTypes[name] = schema, table, "c"
		constraintOccurrences[name] = 1
	}
	manualDefinitions := cloneFinalSchemaColumns(finalManualConstraintDefinitions)
	return FinalCompanyFundSchemaSnapshot{
		OccurrenceSchema: schema,
		OccurrenceTable:  finalCompanyFundTransactionsTable,
		OccurrenceColumns: map[string]string{
			"provider_occurrence_key":               "character varying",
			"provider_occurrence_algorithm_version": "character varying",
		},
		OccurrenceColumnLengths:               map[string]int64{"provider_occurrence_key": 256, "provider_occurrence_algorithm_version": 64},
		OccurrenceColumnsNullable:             map[string]bool{"provider_occurrence_key": true, "provider_occurrence_algorithm_version": true},
		SafeheronOccurrenceIndexName:          finalSafeheronOccurrenceIndexName,
		SafeheronOccurrenceIndexSchema:        schema,
		SafeheronOccurrenceIndexTable:         finalCompanyFundTransactionsTable,
		SafeheronOccurrenceIndexUnique:        true,
		SafeheronOccurrenceIndexValid:         true,
		SafeheronOccurrenceIndexReady:         true,
		SafeheronOccurrenceIndexColumns:       []string{"provider_occurrence_key"},
		SafeheronOccurrenceIndexPredicate:     finalSafeheronOccurrenceIndexPredicate,
		SafeheronOccurrenceIndexDefinition:    "CREATE UNIQUE INDEX " + finalSafeheronOccurrenceIndexName + " ON public.company_fund_transactions USING btree (provider_occurrence_key) WHERE (channel = 'SAFEHERON' AND provider_occurrence_key IS NOT NULL)",
		SafeheronRequiredConstraintName:       migration053ConstraintName,
		SafeheronRequiredConstraintDefinition: finalSafeheronRequiredConstraintDefinition,
		SafeheronRequiredConstraintValidated:  true,
		ManualConstraintNames: []string{
			"company_fund_transactions_usd_valuation_source_check",
			"company_fund_valuation_history_source_check",
			"company_fund_valuation_history_manual_metadata_check",
		},
		ConstraintSchemas:           constraintSchemas,
		ConstraintTables:            constraintTables,
		ConstraintTypes:             constraintTypes,
		ConstraintOccurrences:       constraintOccurrences,
		ManualConstraintDefinitions: manualDefinitions,
		ManualConstraintsValidated: map[string]bool{
			"company_fund_transactions_usd_valuation_source_check": true,
			"company_fund_valuation_history_source_check":          true,
			"company_fund_valuation_history_manual_metadata_check": true,
		},
		ManualProjectionFunctionSchema:        schema,
		ManualProjectionFunctionName:          finalManualProjectionFunctionName,
		ManualProjectionFunctionArgumentCount: 0,
		ManualProjectionFunctionResult:        "trigger",
		ManualProjectionFunctionLanguage:      "plpgsql",
		ManualProjectionFunctionKind:          "f",
		ManualProjectionFunctionOID:           42,
		ManualProjectionFunctionSource:        finalManualProjectionFunctionSource,
		ManualProjectionTriggerSchema:         schema,
		ManualProjectionTriggerTable:          finalCompanyFundTransactionsTable,
		ManualProjectionTriggerFunctionSchema: schema,
		ManualProjectionTriggerFunctionName:   finalManualProjectionFunctionName,
		ManualProjectionTriggerFunctionOID:    42,
		ManualProjectionTriggerEnabled:        "O",
		ManualProjectionTriggerType:           finalManualProjectionTriggerType,
		ManualProjectionTriggerColumns:        append([]string(nil), migration052ProtectedProjectionColumns...),
	}
}

func TestMigration052CanonicalExtractionFailsClosed(t *testing.T) {
	if _, err := extractMigration052ConstraintDefinition("", finalManualConstraintNames[0]); err == nil {
		t.Fatal("missing constraint accepted")
	}
	for _, ddl := range []string{"", "CREATE OR REPLACE FUNCTION " + finalManualProjectionFunctionName + "()", "CREATE OR REPLACE FUNCTION " + finalManualProjectionFunctionName + "() AS $$ BEGIN"} {
		if _, err := extractMigration052FunctionSource(ddl); err == nil {
			t.Fatalf("incomplete function accepted: %q", ddl)
		}
	}
	for _, call := range []func(){
		func() { mustExtractMigration052ConstraintDefinitions("") },
		func() { mustExtractMigration052FunctionSource("") },
	} {
		func() {
			defer func() {
				if recover() == nil {
					t.Fatal("invalid canonical extraction did not panic at initialization boundary")
				}
			}()
			call()
		}()
	}
}

func TestFinalCatalogCanonicalizationRejectsSameTokenBooleanRegrouping(t *testing.T) {
	for _, testCase := range []struct {
		name   string
		mutate func(*FinalCompanyFundSchemaSnapshot)
	}{
		{name: "053 required occurrence", mutate: func(value *FinalCompanyFundSchemaSnapshot) {
			value.SafeheronRequiredConstraintDefinition = "CHECK ((channel <> 'SAFEHERON' OR provider_occurrence_key IS NOT NULL) AND btrim(provider_occurrence_key) <> '' AND provider_occurrence_algorithm_version = 'safeheron-occurrence-v1')"
		}},
		{name: "transaction source", mutate: regroupSourceConstraint("company_fund_transactions_usd_valuation_source_check")},
		{name: "history source", mutate: regroupSourceConstraint("company_fund_valuation_history_source_check")},
		{name: "history MANUAL metadata", mutate: func(value *FinalCompanyFundSchemaSnapshot) {
			definition := value.ManualConstraintDefinitions["company_fund_valuation_history_manual_metadata_check"]
			definition = strings.Replace(definition, "usd_valuation_source IS DISTINCT FROM 'MANUAL' OR ( usd_valuation_method = 'MANUAL_TOTAL' AND", "(usd_valuation_source IS DISTINCT FROM 'MANUAL' OR usd_valuation_method = 'MANUAL_TOTAL') AND", 1)
			value.ManualConstraintDefinitions["company_fund_valuation_history_manual_metadata_check"] = definition
		}},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			candidate := validFinalCompanyFundSchemaSnapshot()
			testCase.mutate(&candidate)
			if _, err := BuildFinalCompanyFundSchemaFingerprint(candidate); err == nil {
				t.Fatal("same-token boolean regrouping was accepted")
			}
		})
	}
}

func regroupSourceConstraint(name string) func(*FinalCompanyFundSchemaSnapshot) {
	return func(value *FinalCompanyFundSchemaSnapshot) {
		definition := value.ManualConstraintDefinitions[name]
		definition = strings.Replace(definition, "usd_valuation_source IS NULL OR usd_valuation_source IN", "(usd_valuation_source IS NULL OR usd_valuation_source) IN", 1)
		value.ManualConstraintDefinitions[name] = definition
	}
}

func TestFinalCatalogCanonicalizationAcceptsPostgresFormattingWithoutErasingGrouping(t *testing.T) {
	postgresRequired := `CHECK ((((channel)::text <> 'SAFEHERON'::text) OR ((provider_occurrence_key IS NOT NULL) AND (btrim((provider_occurrence_key)::text) <> ''::text) AND ((provider_occurrence_algorithm_version)::text = 'safeheron-occurrence-v1'::text))))`
	if normalizeFinalCatalogExpression(postgresRequired) != normalizeFinalCatalogExpression(finalSafeheronRequiredConstraintDefinition) {
		t.Fatal("PostgreSQL required-check formatting did not canonicalize to migration 053")
	}

	postgresSource := `CHECK (((usd_valuation_source IS NULL) OR ((usd_valuation_source)::text = ANY (ARRAY['SAFEHERON'::character varying, 'AIRWALLEX'::character varying, 'COINGECKO'::character varying, 'USD_PAR'::character varying, 'MANUAL'::character varying]::text[]))))`
	expectedSource := finalManualConstraintDefinitions["company_fund_transactions_usd_valuation_source_check"]
	if normalizeFinalCatalogExpression(postgresSource) != normalizeFinalCatalogExpression(expectedSource) {
		t.Fatalf("PostgreSQL ANY/cast formatting did not canonicalize to migration 052:\n%s\n%s", normalizeFinalCatalogExpression(postgresSource), normalizeFinalCatalogExpression(expectedSource))
	}

	postgresIndex := `CREATE UNIQUE INDEX idx_company_fund_transactions_safeheron_occurrence ON public.company_fund_transactions USING btree (provider_occurrence_key) WHERE ((((channel)::text = 'SAFEHERON'::text) AND (provider_occurrence_key IS NOT NULL)))`
	expectedIndex := `CREATE UNIQUE INDEX idx_company_fund_transactions_safeheron_occurrence ON public.company_fund_transactions USING btree (provider_occurrence_key) WHERE (channel = 'SAFEHERON' AND provider_occurrence_key IS NOT NULL)`
	if normalizeFinalIndexDefinition(postgresIndex) != normalizeFinalIndexDefinition(expectedIndex) {
		t.Fatal("PostgreSQL index formatting did not canonicalize to migration 052")
	}
	if got := normalizeFinalIndexDefinition("CREATE INDEX plain ON public.t (id)"); got != "CREATE INDEX plain ON public.t (id)" {
		t.Fatalf("index without predicate changed: %s", got)
	}
}
