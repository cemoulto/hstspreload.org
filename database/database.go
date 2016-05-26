package database

import (
	"strings"
	"time"

	"golang.org/x/net/context"

	"github.com/chromium/hstspreload.appspot.com/database/gcd"
	"google.golang.org/cloud/datastore"
)

const (
	localProjectID = "hstspreload-local"
	prodProjectID  = "hstspreload"

	batchSize = 450
	timeout   = 10 * time.Second

	domainStateKind = "DomainState"
)

// A Database is an abstraction over Datastore with hstspreload-specific
// database functions.
type Database interface {
	PutStates([]DomainState, func(string, ...interface{})) error
	PutState(DomainState) error
	StateForDomain(string) (DomainState, error)
	AllDomainStates() ([]DomainState, error)
	DomainsWithStatus(PreloadStatus) ([]string, error)
	Autocomplete(string) ([]DomainState, error)
}

// DatastoreBacked is a database backed by a gcd.Backend.
type DatastoreBacked struct {
	backend   gcd.Backend
	projectID string
}

// TempLocalDatabase spin up an local in-memory database based
// on a Google Cloud Datastore emulator.
func TempLocalDatabase() (db DatastoreBacked, shutdown func() error, err error) {
	backend, shutdown, err := gcd.NewLocalBackend()
	return DatastoreBacked{backend, localProjectID}, shutdown, err
}

// ProdDatabase gives a Database that will call out to
// the real production instance of Google Cloud Datastore
func ProdDatabase() (db DatastoreBacked) {
	return DatastoreBacked{gcd.NewProdBackend(), prodProjectID}
}

var blackholeLogf = func(format string, args ...interface{}) {}

// PutStates updates the given domain updates in batches.
// Writes updates to logf in real-time.
func (db DatastoreBacked) PutStates(updates []DomainState, logf func(format string, args ...interface{})) error {
	if len(updates) == 0 {
		logf("No updates.\n")
		return nil
	}

	// Set up the datastore context.
	c, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	client, datastoreErr := db.backend.NewClient(c, db.projectID)
	if datastoreErr != nil {
		return datastoreErr
	}

	putMulti := func(keys []*datastore.Key, values []DomainState) error {
		logf("Updating %d entries...", len(keys))

		if _, err := client.PutMulti(c, keys, values); err != nil {
			logf(" failed.\n")
			return err
		}

		logf(" done.\n")
		return nil
	}

	var keys []*datastore.Key
	var values []DomainState
	for _, state := range updates {
		key := datastore.NewKey(c, domainStateKind, string(state.Name), 0, nil)
		keys = append(keys, key)
		values = append(values, state)

		if len(keys) >= batchSize {
			if err := putMulti(keys, values); err != nil {
				return err
			}
			keys = keys[:0]
			values = values[:0]
		}
	}

	if err := putMulti(keys, values); err != nil {
		return err
	}
	return nil
}

// PutState is a convenience version of PutStates for a single domain.
func (db DatastoreBacked) PutState(update DomainState) error {
	return db.PutStates([]DomainState{update}, blackholeLogf)
}

// statesForQuery returns the states for the given datastore query.
func (db DatastoreBacked) statesForQuery(query *datastore.Query) (states []DomainState, err error) {
	// Set up the datastore context.
	c, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	client, datastoreErr := db.backend.NewClient(c, db.projectID)
	if datastoreErr != nil {
		return states, datastoreErr
	}

	keys, err := client.GetAll(c, query, &states)
	if err != nil {
		return states, err
	}

	for i, key := range keys {
		state := states[i]
		state.Name = key.Name()
		states[i] = state
	}

	return states, nil
}

// domainsForQuery returns the domains that match the given datastore query.
func (db DatastoreBacked) domainsForQuery(query *datastore.Query) (domains []string, err error) {
	// Set up the datastore context.
	c, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	client, datastoreErr := db.backend.NewClient(c, db.projectID)
	if datastoreErr != nil {
		return domains, datastoreErr
	}

	keys, err := client.GetAll(c, query.KeysOnly(), nil)
	if err != nil {
		return domains, err
	}

	for _, key := range keys {
		domain := key.Name()
		domains = append(domains, domain)
	}

	return domains, nil
}

// StateForDomain get the state for the given domain.
// Note that the Name field of `state` will not be set.
func (db DatastoreBacked) StateForDomain(domain string) (state DomainState, err error) {
	// Set up the datastore context.
	c, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	client, datastoreErr := db.backend.NewClient(c, db.projectID)
	if datastoreErr != nil {
		return state, datastoreErr
	}

	key := datastore.NewKey(c, domainStateKind, string(domain), 0, nil)
	getErr := client.Get(c, key, &state)
	if getErr != nil {
		if getErr == datastore.ErrNoSuchEntity {
			return DomainState{Status: StatusUnknown}, nil
		}
		return state, getErr
	}

	return state, nil
}

// AllDomainStates gets the states of all domains in the database.
func (db DatastoreBacked) AllDomainStates() (states []DomainState, err error) {
	return db.statesForQuery(datastore.NewQuery("DomainState"))
}

// DomainsWithStatus returns the domains with the given status in the database.
func (db DatastoreBacked) DomainsWithStatus(status PreloadStatus) (domains []string, err error) {
	return db.domainsForQuery(datastore.NewQuery("DomainState").Filter("Status =", string(status)))
}

// Autocomplete returns the entries that starting with the given prefix.
// It returns the first few, alphabetically, truncated to 5 results.
func (db DatastoreBacked) Autocomplete(prefix string) ([]DomainState, error) {
	c, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	key := datastore.NewKey(c, domainStateKind, prefix, 0, nil)
	// We explicitly need to specify the order: https://cloud.google.com/appengine/docs/go/datastore/query-restrictions#ordering_of_query_results_is_undefined_when_no_sort_order_is_specified

	ds, err := db.statesForQuery(
		datastore.
			NewQuery("DomainState").
			Filter("__key__ > ", key).
			Limit(5).
			Order("__key__"),
	)
	if err != nil {
		return ds, err
	}
	var output []DomainState
	for _, s := range ds {
		if strings.HasPrefix(s.Name, prefix) {
			output = append(output, s)
		}
	}
	return output, nil
}
