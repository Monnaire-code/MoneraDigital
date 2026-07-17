package fundrouting

import (
	"context"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
)

func TestAlertEscalatorCreatesAtMostOneMissingOpenSLALevel(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	escalator, err := NewAlertEscalator(db)
	if err != nil {
		t.Fatal(err)
	}
	mock.ExpectQuery("INSERT INTO safeheron_transaction_routing_alerts").
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(8))
	processed, err := escalator.ProcessOne(context.Background())
	if err != nil || !processed {
		t.Fatalf("ProcessOne = %v, %v", processed, err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestAlertEscalatorReturnsIdleWhenNoThresholdIsDue(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	escalator, _ := NewAlertEscalator(db)
	mock.ExpectQuery("INSERT INTO safeheron_transaction_routing_alerts").
		WillReturnRows(sqlmock.NewRows([]string{"id"}))
	processed, err := escalator.ProcessOne(context.Background())
	if err != nil || processed {
		t.Fatalf("ProcessOne = %v, %v", processed, err)
	}
}
