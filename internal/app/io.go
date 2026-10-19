package app

import (
	"encoding/json"
	"fmt"
	"github.com/WhitenWhiten/topophany/internal/ioaccount"
)

func (a *App) logIO() {
	a.logMemory()
	b, _ := json.Marshal(ioaccount.Read())
	fmt.Fprintf(a.Err, "storage_io_since_start %s\n", b)
}
