package app

import (
	"path/filepath"
	"sync"
	"testing"

	"github.com/WhitenWhiten/topophany/internal/api"
	"github.com/WhitenWhiten/topophany/internal/client"
	"github.com/WhitenWhiten/topophany/internal/config"
	"github.com/WhitenWhiten/topophany/internal/secure"
)

func TestConcurrentInitHasExactlyOneWinner(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "nested", "demo")
	start := make(chan struct{})
	results := make(chan error, 16)
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); <-start; _, _, e := invoke("", "init", "--dir", dir); results <- e }()
	}
	close(start)
	wg.Wait()
	close(results)
	winners := 0
	for e := range results {
		if e == nil {
			winners++
		}
	}
	if winners != 1 {
		t.Fatal("initializers overwrote each other", winners)
	}
	c, e := config.Load(filepath.Join(dir, "topology.json"))
	if e != nil {
		t.Fatal(e)
	}
	a, e := api.LoadAuth(filepath.Join(dir, "auth.json"), c)
	if e != nil {
		t.Fatal(e)
	}
	cr, e := client.Load(filepath.Join(dir, "credentials.json"))
	if e != nil {
		t.Fatal(e)
	}
	if secure.Hash(cr.Profiles[cr.Active].Servers[0].Token) != a.Tokens[0].TokenSHA256 {
		t.Fatal("initializer produced mismatched credentials")
	}
}
