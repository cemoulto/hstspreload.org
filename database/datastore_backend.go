package database

import (
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"sync"
	"time"

	"golang.org/x/net/context"
	"google.golang.org/cloud"
	"google.golang.org/cloud/datastore"
	"google.golang.org/grpc"
)

const (
	// A blank project ID forces the project ID to be read from
	// the DATASTORE_PROJECT_ID environment variable.
	projectID = ""
)

/******** Backends ********/

// LocalDatastore represents an emulated Google Cloud Datastore
// running on localhost
type LocalDatastore struct {
	// unexported fields
	addr string
	cmd  *exec.Cmd
}

// ProdDatastore represent the production instance of
// Google Cloud Datastore
type ProdDatastore struct{}

// DatastoreBackend is an abstraction over {Local, Prod}Datastore
// that allows callers to construct a new client without having to
// know about whether it's local.
type DatastoreBackend interface {
	NewClient(ctx context.Context) (*datastore.Client, error)
}

/******** Port assignment for local backends ********/

var (
	portMutex sync.Mutex
	nextPort  = 9001
)

func portString() string {
	// TODO: Check that the port is available?
	portMutex.Lock()
	port := nextPort
	nextPort++
	portMutex.Unlock()

	return strconv.Itoa(port)
}

/******** LocalDatastore ********/

// NewLocalDatastore spawns a new LocalDatastore using Java.
// When there is no error, make sure to call shutdown() in order to
// terminate the Java process.
func NewLocalDatastore() (db LocalDatastore, shutdown func(), err error) {
	db = LocalDatastore{}

	ps := portString()

	db.addr = "localhost:" + ps

	cmd := exec.Command(
		"java",
		"-cp",
		"./testing/gcd/CloudDatastore.jar",
		"com.google.cloud.datastore.emulator.CloudDatastore",
		"[datastore.go]",
		"start",
		"-p",
		ps, "--testing",
	)
	db.cmd = cmd

	err = cmd.Start()
	if err != nil {
		return db, func() {}, nil
	}

	shutdown = func() {
		cmd.Process.Kill()
	}

	// Wait for the server to start. 1000ms seems to work.
	time.Sleep(1000 * time.Millisecond)
	return db, shutdown, nil
}

// NewClient constructs a datastore client for the emulated LocalDatastore.
// The constructed client will work offline and never connect to the wide internet.
func (db LocalDatastore) NewClient(ctx context.Context) (*datastore.Client, error) {
	projectID := "hstspreload-local-testing"

	// The code below is based closely on the implementation of
	//  datastore.NewClient().

	if db.addr == "" {
		return nil, errors.New("Empty addr. Uninitialized local backend?")
	}

	conn, err := grpc.Dial(db.addr, grpc.WithInsecure())
	if err != nil {
		return nil, fmt.Errorf("grpc.Dial: %v", err)
	}

	var o []cloud.ClientOption
	o = []cloud.ClientOption{cloud.WithBaseGRPC(conn)}
	client, err := datastore.NewClient(ctx, projectID, o...)
	if err != nil {
		return nil, err
	}
	return client, nil
}

/******** ProdDatastore ********/

// NewProdDatastore construct a new ProdDatastore.
func NewProdDatastore() (db ProdDatastore) {
	// No special configuration in this case.
	return ProdDatastore{}
}

// NewClient is a wrapper around the default implementation of
// datastore.NewClient(), calling out to the real, live
// Google Cloud Datastore.
func (db ProdDatastore) NewClient(ctx context.Context) (*datastore.Client, error) {
	return datastore.NewClient(ctx, projectID)
}
