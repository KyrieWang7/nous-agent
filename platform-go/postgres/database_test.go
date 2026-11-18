package postgres

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
)

func TestGORMBindsRepeatedParametersAndPreservesSQL(t *testing.T) {
	connection, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	db, err := FromSQL(connection)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	mock.ExpectQuery(`SELECT \$1::text,\$2::text,'\$1',\$\$\$2\$\$,doc \? 'key' /\* \$9 \*/ FROM records WHERE id=\$3`).WithArgs("value", "value", 7).WillReturnRows(sqlmock.NewRows([]string{"value"}).AddRow("ok"))
	var value string
	err = db.QueryRow(context.Background(), `SELECT $2::text,$2::text,'$1',$$$2$$,doc ? 'key' /* $9 */ FROM records WHERE id=$1`, 7, "value").Scan(&value)
	if err != nil || value != "ok" {
		t.Fatalf("value=%q err=%v", value, err)
	}
	mock.ExpectExec(`UPDATE records SET value=\$1 WHERE id=\$2 OR parent_id=\$3`).WithArgs("x", 3, 3).WillReturnResult(sqlmock.NewResult(0, 2))
	result, err := db.Exec(context.Background(), `UPDATE records SET value=$2 WHERE id=$1 OR parent_id=$1`, 3, "x")
	if err != nil || result.RowsAffected() != 2 {
		t.Fatalf("rows=%d err=%v", result.RowsAffected(), err)
	}
	mock.ExpectQuery(`SELECT id FROM records WHERE id=\$1`).WithArgs(9).WillReturnRows(sqlmock.NewRows([]string{"id"}))
	if err := db.QueryRow(context.Background(), `SELECT id FROM records WHERE id=$1`, 9).Scan(&value); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("missing row: %v", err)
	}
	if err := db.QueryRow(context.Background(), `SELECT $2`, 1).Scan(&value); err == nil {
		t.Fatal("missing parameter silently accepted")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestGORMTransactionRollsBackFailedBatch(t *testing.T) {
	connection, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	db, err := FromSQL(connection)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	failed := errors.New("write failed")
	mock.ExpectBegin()
	mock.ExpectExec(`INSERT INTO events`).WithArgs("one").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(`INSERT INTO events`).WithArgs("two").WillReturnError(failed)
	mock.ExpectRollback()
	err = db.Transaction(context.Background(), func(tx *DB) error {
		for _, value := range []string{"one", "two"} {
			if _, err := tx.Exec(context.Background(), `INSERT INTO events(value)VALUES($1)`, value); err != nil {
				return err
			}
		}
		return nil
	})
	if !errors.Is(err, failed) {
		t.Fatalf("error=%v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
