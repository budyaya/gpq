package gpq_test

import (
	"log"
	"os"
	"runtime"
	"runtime/pprof"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/JustinTimperio/gpq"
	"github.com/dgraph-io/badger/v4"
)

func TestGPQ(t *testing.T) {

	var (
		total      int  = 1000000
		print      bool = false
		syncToDisk bool = true
		lazy       bool = true
		batchSize  int  = 1000
		sent       uint64
		timedOut   uint64
		received   uint64
		missed     int64
		hits       int64
	)

	// Create a pprof file
	f, err := os.Create("profile.pprof")
	if err != nil {
		log.Fatal(err)
	}
	defer f.Close()

	// Start CPU profiling
	err = pprof.StartCPUProfile(f)
	if err != nil {
		log.Fatal(err)
	}
	defer pprof.StopCPUProfile()

	// Create pprof mutex file
	fm, err := os.Create("profile.mutex")
	if err != nil {
		log.Fatal(err)
	}
	defer fm.Close()

	// Start mutex profiling
	runtime.SetMutexProfileFraction(1)
	defer func() {
		p := pprof.Lookup("mutex")
		if p == nil {
			log.Fatal("could not capture mutex profile")
		}
		// Create pprof mutex file
		fm, err := os.Create("profile.mutex")
		if err != nil {
			log.Fatal(err)
		}
		defer fm.Close()
		if err := p.WriteTo(fm, 0); err != nil {
			log.Fatal("could not write mutex profile: ", err)
		}
	}()

	queue, err := gpq.NewGPQ[int](10, syncToDisk, "/tmp/gpq/test", lazy, int64(batchSize))
	if err != nil {
		log.Fatalln(err)
	}
	wg := &sync.WaitGroup{}

	go func() {
		for atomic.LoadUint64(&queue.NonEmptyBuckets.ObjectsInQueue) > 0 || atomic.LoadUint64(&received) < uint64(total) {
			time.Sleep(500 * time.Millisecond)
			to, es, err := queue.Prioritize()
			if err != nil {
			}
			atomic.AddUint64(&timedOut, to)
			log.Println("Prioritize Timed Out:", to, "Escalated:", es)
		}

	}()

	timer := time.Now()
	wg.Add(10)
	for i := 0; i < 10; i++ {
		go func() {
			defer wg.Done()
			for i := 0; i < total/10; i++ {
				p := i % 10
				timer := time.Now()
				err := queue.EnQueue(
					i,
					int64(p),
					true,
					time.Duration(time.Second),
					true,
					time.Duration(time.Second*10),
				)
				if err != nil {
					log.Fatalln(err)
				}
				if print {
					log.Println("EnQueue", p, time.Since(timer))
				}
				atomic.AddUint64(&sent, 1)
			}
		}()
	}

	wg.Add(2)
	for i := 0; i < 2; i++ {
		go func() {
			defer wg.Done()

			var lastPriority int64

			for atomic.LoadUint64(&queue.NonEmptyBuckets.ObjectsInQueue) > 0 || atomic.LoadUint64(&received)+atomic.LoadUint64(&timedOut) < uint64(total) {
				timer := time.Now()
				priority, item, err := queue.DeQueue()
				if err != nil {
					if print {
						log.Println("Hits", hits, "Misses", missed, "Sent", sent, "Received", missed+hits, err)
					}
					time.Sleep(10 * time.Millisecond)
					lastPriority = 0
					continue
				}
				atomic.AddUint64(&received, 1)
				if print {
					log.Println("DeQueue", priority, received, item, time.Since(timer))
				}

				if lastPriority > priority {
					missed++
				} else {
					hits++
				}
				lastPriority = priority
			}
			time.Sleep(500 * time.Millisecond)
		}()
	}

	wg.Wait()
	log.Println("Sent", atomic.LoadUint64(&sent), "Received", atomic.LoadUint64(&received), "Timed Out", atomic.LoadUint64(&timedOut), "Finished in", time.Since(timer), "Missed", missed, "Hits", hits)

	// Wait for all db sessions to sync to disk
	queue.Close()

}

func TestNumberOfItems(t *testing.T) {
	var total int
	opts := badger.DefaultOptions("/tmp/gpq/test")
	opts.Logger = nil
	db, err := badger.Open(opts)
	if err != nil {
		t.Fatal(err)
	}

	err = db.View(func(txn *badger.Txn) error {
		opts := badger.DefaultIteratorOptions
		opts.PrefetchSize = 10
		it := txn.NewIterator(opts)
		defer it.Close()
		for it.Rewind(); it.Valid(); it.Next() {
			total++
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Log("Total items in badgerDB", total)
	return
}
