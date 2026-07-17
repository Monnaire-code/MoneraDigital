package container

import (
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"monera-digital/internal/fundrouting"
)

func TestFinalizeSafeheronRoutingRespectsGlobalWorkerSwitch(t *testing.T) {
	db, _, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	c := &Container{
		DB:                   db,
		SafeheronRoutingMode: fundrouting.ModeRoutingAuthoritative,
		companyFundRuntimeConfig: companyFundRuntimeConfig{
			StartBackgroundWorkers: false,
		},
	}
	finalizeSafeheronRouting(c)
	if c.FundRoutingWorker != nil || c.FundRoutingProjectionWorker != nil || c.FundRoutingReconciler != nil {
		t.Fatal("routing workers must remain disabled while the global worker switch is off")
	}
}
