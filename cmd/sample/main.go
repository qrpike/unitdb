package main

import (
	"fmt"
	"log"

	"github.com/unit-io/unitdb"
)

func main() {
	// Opening a database.
	// Open DB with Mutable flag to allow deleting messages
	db, err := unitdb.Open("example", unitdb.WithDefaultOptions(), unitdb.WithMutable())
	if err != nil {
		log.Fatal(err)
		return
	}
	defer db.Close()

	// Use Entry.WithPayload() method to bulk store messages as topic is parsed one time on first request.
	topic := []byte("teams.alpha.ch1.u1")
	entry := &unitdb.Entry{Topic: topic}
	for j := 0; j < 50; j++ {
		db.PutEntry(entry.WithPayload([]byte(fmt.Sprintf("msg for team alpha channel1 receiver1 #%2d", j))))
	}

	if msgs, err := db.Get(unitdb.NewQuery([]byte("teams.alpha.ch1.u1?last=1h")).WithLimit(100)); err == nil {
		for _, msg := range msgs {
			log.Printf("%s ", msg)
		}
	}

	// Writing to single topic in a batch
	err = db.Batch(func(b *unitdb.Batch, completed <-chan struct{}) error {
		topic := []byte("teams.alpha.ch1.*?ttl=1h")
		b.Put(topic, []byte("msg for team alpha channel1 all receivers"))
		return nil
	})
	if err != nil {
		log.Fatal(err)
		return
	}

	if msgs, err := db.Get(unitdb.NewQuery([]byte("teams.alpha.ch1.u2?last=1h")).WithLimit(10)); err == nil {
		for _, msg := range msgs {
			log.Printf("%s ", msg)
		}
	}
	if msgs, err := db.Get(unitdb.NewQuery([]byte("teams.alpha.ch1.u3?last=1h")).WithLimit(10)); err == nil {
		for _, msg := range msgs {
			log.Printf("%s ", msg)
		}
	}

	// Writing to multiple topics in a batch
	err = db.Batch(func(b *unitdb.Batch, completed <-chan struct{}) error {
		b.PutEntry(unitdb.NewEntry([]byte("teams.alpha.ch1.u2"), []byte("msg for team alpha channel1 receiver2")))
		b.PutEntry(unitdb.NewEntry([]byte("teams.alpha.ch1.u3"), []byte("msg for team alpha channel1 receiver3")))
		return nil
	})
	if err != nil {
		log.Fatal(err)
		return
	}

	if msgs, err := db.Get(unitdb.NewQuery([]byte("teams.alpha.ch1.u2?last=1h")).WithLimit(10)); err == nil {
		for _, msg := range msgs {
			log.Printf("%s ", msg)
		}
	}
	if msgs, err := db.Get(unitdb.NewQuery([]byte("teams.alpha.ch1.u3?last=1h")).WithLimit(10)); err == nil {
		for _, msg := range msgs {
			log.Printf("%s ", msg)
		}
	}

	// Topic isolation can be achieved using Contract while putting messages into unitdb and querying messages from a topic.
	// Use DB.NewContract() to generate a new Contract and then specify Contract while putting messages using Batch.PutEntry() function.
	contract, err := db.NewContract()

	// Writing to single topic in a batch
	err = db.Batch(func(b *unitdb.Batch, completed <-chan struct{}) error {
		b.SetOptions(unitdb.WithBatchContract(contract))
		topic := []byte("teams.alpha.ch1.*?ttl=1h")
		b.Put(topic, []byte("msg for team alpha channel1 all receivers #1"))
		b.Put(topic, []byte("msg for team alpha channel1 all receivers #2"))
		b.Put(topic, []byte("msg for team alpha channel1 all receivers #3"))
		return nil
	})

	// Writing to multiple topics in a batch
	err = db.Batch(func(b *unitdb.Batch, completed <-chan struct{}) error {
		b.SetOptions(unitdb.WithBatchContract(contract))
		b.PutEntry(unitdb.NewEntry([]byte("teams.*.ch1"), []byte("msg for any team channel1")))
		b.PutEntry(unitdb.NewEntry([]byte("teams.alpha.*"), []byte("msg for team alpha all channels")))
		b.PutEntry(unitdb.NewEntry([]byte("teams..."), []byte("msg for all teams and all channels")))
		b.PutEntry(unitdb.NewEntry([]byte("..."), []byte("msg broadcast to all receivers of all teams all channels")))
		return nil
	})
	if err != nil {
		log.Fatal(err)
		return
	}

	// Get message for team alpha channel1
	if msgs, err := db.Get(unitdb.NewQuery([]byte("teams.alpha.ch1?last=1h")).WithLimit(10)); err == nil {
		for _, msg := range msgs {
			log.Printf("%s ", msg)
		}
	}

	// Get message for team beta channel1
	if msgs, err := db.Get(unitdb.NewQuery([]byte("teams.beta.ch1?last=1h")).WithLimit(10)); err == nil {
		for _, msg := range msgs {
			log.Printf("%s ", msg)
		}
	}

	// Get message for team beta channel2 receiver1
	if msgs, err := db.Get(unitdb.NewQuery([]byte("teams.beta.ch2.u1?last=1h")).WithLimit(10)); err == nil {
		for _, msg := range msgs {
			log.Printf("%s ", msg)
		}
	}

	// Set encryption flag in batch options to encrypt all messages in a batch.
	// Note, encryption can also be set on entire database using DB.Open() and set encryption flag in options parameter.
	err = db.Batch(func(b *unitdb.Batch, completed <-chan struct{}) error {
		b.SetOptions(unitdb.WithBatchEncryption())
		topic := []byte("teams.alpha.ch1.u1?ttl=1h")
		b.Put(topic, []byte("msg for team alpha channel1 receiver1"))
		return nil
	})

	if err != nil {
		log.Fatal(err)
		return
	}
}
