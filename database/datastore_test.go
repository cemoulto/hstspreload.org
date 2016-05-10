package database

import "fmt"

func ExampleNewLocalDatastore() {
	db, shutdown, err := NewLocalDatastore()
	if err != nil {
		fmt.Printf("%s", s)
	}
	defer shutdown()

	PutState(db, DomainState{
		Name:   "garron.net",
		Status: StatusRejected,
	})
}
