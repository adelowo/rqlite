package store

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"sync"
	"time"

	sdb "github.com/rqlite/rqlite/db"
)

// Connection is a connection to the database.
type Connection struct {
	db    *sdb.Conn // Connection to SQLite database.
	store *Store    // Store to apply commands to.
	id    uint64    // Connection ID, used as a handle by clients.

	timeMu     sync.Mutex
	createdAt  time.Time
	lastUsedAt time.Time

	txStateMu   sync.Mutex
	txStartedAt time.Time

	logger *log.Logger
}

// NewConnection returns a connection to the database.
func NewConnection(c *sdb.Conn, s *Store, id uint64) *Connection {
	return &Connection{
		db:        c,
		store:     s,
		id:        id,
		createdAt: time.Now(),
		logger:    log.New(os.Stderr, "[connection] ", log.LstdFlags),
	}
}

// ID returns the ID of the connection.
func (c *Connection) ID() uint64 {
	return c.id
}

// String implements the Stringer interface on the Connection.
func (c *Connection) String() string {
	return fmt.Sprintf("connection:%d", c.id)
}

// Execute executes queries that return no rows, but do modify the database.
func (c *Connection) Execute(ex *ExecuteRequest) (*ExecuteResponse, error) {
	return c.store.execute(c, ex)
}

// ExecuteOrAbort executes the requests, but aborts any active transaction
// on the underlying database in the case of any error.
func (c *Connection) ExecuteOrAbort(ex *ExecuteRequest) (resp *ExecuteResponse, retErr error) {
	return c.store.executeOrAbort(c, ex)
}

// Query executes queries that return rows, and do not modify the database.
func (c *Connection) Query(qr *QueryRequest) (*QueryResponse, error) {
	return c.store.query(c, qr)
}

// AbortTransaction aborts -- rolls back -- any active transaction. Calling code
// should know exactly what it is doing if it decides to call this function. It
// can be used to clean up any dangling state that may result from certain
// error scenarios.
func (c *Connection) AbortTransaction() error {
	_, err := c.store.execute(c, &ExecuteRequest{[]string{"ROLLBACK"}, false, false})
	return err
}

// Close closes the connection.
func (c *Connection) Close() error {
	return c.store.disconnect(c)
}

// MarshalJSON implements the JSON Marshaler interface.
func (c *Connection) MarshalJSON() ([]byte, error) {
	fk, err := c.db.FKConstraints()
	if err != nil {
		return nil, err
	}

	m := make(map[string]interface{})
	m["fk_constraints"] = enabledFromBool(fk)
	m["id"] = c.id
	m["created_at"] = c.createdAt
	if !c.txStartedAt.IsZero() {
		m["tx_started_at"] = c.txStartedAt
	}
	if !c.lastUsedAt.IsZero() {
		m["last_used_at"] = c.lastUsedAt
	}

	return json.Marshal(m)
}

// TxStateChange is a helper that detects when the transaction state on a
// connection changes.
type TxStateChange struct {
	c    *Connection
	tx   bool
	done bool
}

// NewTxStateChange returns an initialized TxStateChange
func NewTxStateChange(c *Connection) *TxStateChange {
	return &TxStateChange{
		c:  c,
		tx: c.db.TransactionActive(),
	}
}

// CheckAndSet sets whether a transaction has begun or ended on the
// connection since the TxStateChange was created. Once CheckAndSet
// has been called, this function will panic if called a second time.
func (t *TxStateChange) CheckAndSet() {
	t.c.txStateMu.Lock()
	defer t.c.txStateMu.Unlock()
	defer func() { t.done = true }()

	if t.done {
		panic("CheckAndSet should only be called once")
	}

	if !t.tx && t.c.db.TransactionActive() && t.c.txStartedAt.IsZero() {
		t.c.txStartedAt = time.Now()
	} else if t.tx && !t.c.db.TransactionActive() && !t.c.txStartedAt.IsZero() {
		t.c.txStartedAt = time.Time{}
	}
}
