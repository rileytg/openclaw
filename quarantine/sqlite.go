package main

/*
#cgo LDFLAGS: -lsqlite3
#include <sqlite3.h>
#include <stdlib.h>
*/
import "C"
import (
	"fmt"
	"unsafe"
)

// DB is a minimal CGO wrapper around sqlite3.
type DB struct {
	handle *C.sqlite3
}

// OpenDB opens a SQLite database at the given path.
func OpenDB(path string) (*DB, error) {
	cPath := C.CString(path)
	defer C.free(unsafe.Pointer(cPath))

	var handle *C.sqlite3
	rc := C.sqlite3_open(cPath, &handle)
	if rc != C.SQLITE_OK {
		msg := C.GoString(C.sqlite3_errmsg(handle))
		C.sqlite3_close(handle)
		return nil, fmt.Errorf("sqlite3_open: %s", msg)
	}

	db := &DB{handle: handle}

	// Enable WAL mode.
	if err := db.Exec("PRAGMA journal_mode=WAL"); err != nil {
		db.Close()
		return nil, err
	}
	if err := db.Exec("PRAGMA foreign_keys=ON"); err != nil {
		db.Close()
		return nil, err
	}

	return db, nil
}

// Exec runs a SQL statement that doesn't return rows.
func (db *DB) Exec(sql string) error {
	cSQL := C.CString(sql)
	defer C.free(unsafe.Pointer(cSQL))

	var errMsg *C.char
	rc := C.sqlite3_exec(db.handle, cSQL, nil, nil, &errMsg)
	if rc != C.SQLITE_OK {
		msg := C.GoString(errMsg)
		C.sqlite3_free(unsafe.Pointer(errMsg))
		return fmt.Errorf("sqlite3_exec: %s", msg)
	}
	return nil
}

// Stmt is a prepared statement.
type Stmt struct {
	handle *C.sqlite3_stmt
	db     *DB
}

// Prepare compiles a SQL statement.
func (db *DB) Prepare(sql string) (*Stmt, error) {
	cSQL := C.CString(sql)
	defer C.free(unsafe.Pointer(cSQL))

	var handle *C.sqlite3_stmt
	rc := C.sqlite3_prepare_v2(db.handle, cSQL, -1, &handle, nil)
	if rc != C.SQLITE_OK {
		msg := C.GoString(C.sqlite3_errmsg(db.handle))
		return nil, fmt.Errorf("sqlite3_prepare: %s", msg)
	}
	return &Stmt{handle: handle, db: db}, nil
}

// BindText binds a text value at 1-based index.
func (s *Stmt) BindText(idx int, val string) {
	cVal := C.CString(val)
	C.sqlite3_bind_text(s.handle, C.int(idx), cVal, C.int(len(val)), (*[0]byte)(C.free))
}

// BindNull binds NULL at 1-based index.
func (s *Stmt) BindNull(idx int) {
	C.sqlite3_bind_null(s.handle, C.int(idx))
}

// BindTextOrNull binds text if non-empty, NULL otherwise.
func (s *Stmt) BindTextOrNull(idx int, val string) {
	if val == "" {
		s.BindNull(idx)
	} else {
		s.BindText(idx, val)
	}
}

// Step executes one step. Returns true if there's a row, false if done.
func (s *Stmt) Step() (bool, error) {
	rc := C.sqlite3_step(s.handle)
	if rc == C.SQLITE_ROW {
		return true, nil
	}
	if rc == C.SQLITE_DONE {
		return false, nil
	}
	msg := C.GoString(C.sqlite3_errmsg(s.db.handle))
	return false, fmt.Errorf("sqlite3_step: %s", msg)
}

// ColumnText returns the text value of column at 0-based index.
func (s *Stmt) ColumnText(idx int) string {
	cText := C.sqlite3_column_text(s.handle, C.int(idx))
	if cText == nil {
		return ""
	}
	return C.GoString((*C.char)(unsafe.Pointer(cText)))
}

// ColumnInt returns the int value of column at 0-based index.
func (s *Stmt) ColumnInt(idx int) int {
	return int(C.sqlite3_column_int(s.handle, C.int(idx)))
}

// Reset resets the statement for re-use.
func (s *Stmt) Reset() {
	C.sqlite3_reset(s.handle)
	C.sqlite3_clear_bindings(s.handle)
}

// Finalize frees the statement.
func (s *Stmt) Finalize() {
	if s.handle != nil {
		C.sqlite3_finalize(s.handle)
		s.handle = nil
	}
}

// Changes returns the number of rows changed by the last statement.
func (db *DB) Changes() int {
	return int(C.sqlite3_changes(db.handle))
}

// Close closes the database.
func (db *DB) Close() error {
	if db.handle != nil {
		rc := C.sqlite3_close(db.handle)
		if rc != C.SQLITE_OK {
			return fmt.Errorf("sqlite3_close: code %d", rc)
		}
		db.handle = nil
	}
	return nil
}
