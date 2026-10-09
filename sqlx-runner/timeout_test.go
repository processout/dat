package runner

import (
	"database/sql"
	"database/sql/driver"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/mgutz/jo/v1"
	"github.com/processout/dat"
	"gopkg.in/stretchr/testify.v1/assert"
)

type TimeoutPerson struct {
	Name string `db:"name" json:"name"`
	Age  int    `db:"age" json:"age"`
	NA   string `db:"na" json:"na"`
}

func TestTimeoutExec(t *testing.T) {
	_, err := testDB.SQL("SELECT pg_sleep(1)").Timeout(10 * time.Millisecond).Exec()
	assert.Equal(t, err, dat.ErrTimedout)

	// test no timeout
	result, err := testDB.SQL("SELECT 0").Timeout(3 * time.Second).Exec()
	assert.Equal(t, int64(1), result.RowsAffected)
	assert.NoError(t, err)
}

func TestTimeoutScalar(t *testing.T) {
	var s string
	var n int
	err := testDB.SQL("SELECT pg_sleep(2) as sleep, 1 as k;").Timeout(10*time.Millisecond).QueryScalar(&s, &n)
	assert.Equal(t, dat.ErrTimedout, err)

	// test no timeout
	err = testDB.SQL("SELECT 1 as k").Timeout(10 * time.Millisecond).QueryScalar(&n)
	assert.NoError(t, err)
	assert.Equal(t, 1, n)
}

func TestTimeoutSlice(t *testing.T) {
	var s string
	var arr []int
	err := testDB.SQL("SELECT pg_sleep(2) as sleep, 1 as k;").Timeout(10*time.Millisecond).QueryScalar(&s, &arr)
	assert.Equal(t, dat.ErrTimedout, err)

	// test no timeout
	err = testDB.SQL("SELECT * FROM generate_series(1, 3)").Timeout(1 * time.Second).QuerySlice(&arr)
	assert.NoError(t, err)
	assert.Equal(t, []int{1, 2, 3}, arr)
}

func TestTimeoutStruct(t *testing.T) {
	var person TimeoutPerson

	err := testDB.SQL("SELECT pg_sleep(2) as na, 'timeout' as name;").Timeout(10 * time.Millisecond).QueryStruct(&person)
	assert.Equal(t, dat.ErrTimedout, err)

	// test no timeout
	err = testDB.SQL("SELECT 'john' as name, 10 as age").Timeout(1 * time.Second).QueryStruct(&person)
	assert.NoError(t, err)
	assert.Equal(t, "john", person.Name)
	assert.Equal(t, 10, person.Age)
}

func TestTimeoutStructs(t *testing.T) {
	var people []TimeoutPerson

	err := testDB.SQL("SELECT pg_sleep(2) as na, 'timeout' as name;").Timeout(10 * time.Millisecond).QueryStructs(&people)
	assert.Equal(t, dat.ErrTimedout, err)

	// test no timeout
	err = testDB.SQL("SELECT 'john' as name, 10 as age UNION ALL SELECT 'jane' as name, 11 as age").Timeout(1 * time.Second).QueryStructs(&people)
	assert.NoError(t, err)
	assert.Equal(t, 2, len(people))
	assert.Equal(t, "john", people[0].Name)
	assert.Equal(t, "jane", people[1].Name)
}

func TestTimeoutObject(t *testing.T) {
	var person jo.Object

	err := testDB.SQL("SELECT pg_sleep(2) as na, 'timeout' as name").Timeout(10 * time.Millisecond).QueryObject(&person)
	assert.Equal(t, dat.ErrTimedout, err)

	// test no timeout
	err = testDB.SQL("SELECT 'john' as name, 10 as age LIMIT 1").Timeout(1 * time.Second).QueryObject(&person)
	assert.NoError(t, err)
	assert.Equal(t, "john", person.AsString("[0].name"))
	assert.Equal(t, 10, person.AsInt("[0].age"))
}

func TestTimeoutJSON(t *testing.T) {
	b, err := testDB.SQL("SELECT pg_sleep(2) as na, 'timeout' as name").Timeout(10 * time.Millisecond).QueryJSON()
	assert.Equal(t, dat.ErrTimedout, err)
	assert.Equal(t, []byte(nil), b)

	// test no timeout
	b, err = testDB.SQL("SELECT 'john' as name, 10 as age LIMIT 1").Timeout(1 * time.Second).QueryJSON()
	assert.NoError(t, err)
	obj, _ := jo.NewFromBytes(b)
	assert.Equal(t, "john", obj.AsString("[0].name"))
	assert.Equal(t, 10, obj.AsInt("[0].age"))
}

// tracerCommentPrefix mimics a sqlcommenter-style comment that tracing drivers prepend to every statement.
const tracerCommentPrefix = "/*traceparent='00-0000000000000000000000000000002a-000000000000002a-00'*/ "

const (
	cancelledQueryTimeout = 50 * time.Millisecond
	cancelledQuerySleep   = 5 * time.Second
	backendStopDeadline   = 1 * time.Second
	backendPollInterval   = 20 * time.Millisecond
)

// commentPrefixDriver prepends a comment to every statement, so the dat query ID is no longer at the start of
// the statement text seen in pg_stat_activity.
type commentPrefixDriver struct {
	driver.Driver
}

func (d commentPrefixDriver) Open(name string) (driver.Conn, error) {
	conn, err := d.Driver.Open(name)
	if err != nil {
		return nil, err
	}
	return commentPrefixConn{Conn: conn}, nil
}

// commentPrefixConn only exposes driver.Conn so database/sql always goes through Prepare.
type commentPrefixConn struct {
	driver.Conn
}

func (c commentPrefixConn) Prepare(query string) (driver.Stmt, error) {
	return c.Conn.Prepare(tracerCommentPrefix + query)
}

var registerCommentPrefixDriver sync.Once

const commentPrefixDriverName = "dat-comment-prefix"

func newCommentPrefixDB(t *testing.T) *DB {
	registerCommentPrefixDriver.Do(func() {
		sql.Register(commentPrefixDriverName, commentPrefixDriver{Driver: sqlDB.Driver()})
	})

	db, err := sql.Open(commentPrefixDriverName, os.Getenv("DAT_DSN"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })

	return NewDB(db, "postgres")
}

func countActiveBackends(t *testing.T, marker string) int {
	var count int
	err := testDB.SQL(`
		SELECT count(*)
		FROM pg_stat_activity
		WHERE state = 'active'
		AND pid <> pg_backend_pid()
		AND query LIKE $1`, "%"+marker+"%").QueryScalar(&count)
	if err != nil {
		t.Fatal(err)
	}
	return count
}

func waitForBackendToStop(t *testing.T, marker string) bool {
	deadline := time.Now().Add(backendStopDeadline)
	for time.Now().Before(deadline) {
		if countActiveBackends(t, marker) == 0 {
			return true
		}
		time.Sleep(backendPollInterval)
	}
	return false
}

func TestTimeoutCancelsBackend(t *testing.T) {
	tests := []struct {
		name string
		db   func(t *testing.T) *DB
	}{
		{
			name: "statement starts with query ID",
			db:   func(*testing.T) *DB { return testDB },
		},
		{
			name: "driver prepends a comment before query ID",
			db:   newCommentPrefixDB,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			inputMarker := "dat-cancel-" + uuid()
			db := tt.db(t)

			_, err := db.SQL(fmt.Sprintf("SELECT pg_sleep(%d) -- %s", int(cancelledQuerySleep.Seconds()), inputMarker)).
				Timeout(cancelledQueryTimeout).
				Exec()

			assert.Equal(t, dat.ErrTimedout, err)
			assert.True(t, waitForBackendToStop(t, inputMarker), "query is still running on the server after timeout")
		})
	}
}

func TestCancelQuerySQLDoesNotCancelItself(t *testing.T) {
	_, err := testDB.SQL(cancelQuerySQL(uuid())).Exec()

	assert.NoError(t, err)
}
