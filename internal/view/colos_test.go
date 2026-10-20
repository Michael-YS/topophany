package view

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"regexp"
	"sort"
	"strconv"
	"testing"
)

// The capture the generated table must still describe: Cloudflare's public
// locations list, saved byte for byte so the header's hash can be checked
// offline.
const coloSnapshot = "testdata/cloudflare-locations-2026-10-19.json"

func TestGeneratedColosMatchTheirSnapshot(t *testing.T) {
	raw, err := os.ReadFile(coloSnapshot)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(raw)
	digest := hex.EncodeToString(sum[:])

	generated, err := os.ReadFile("colos.go")
	if err != nil {
		t.Fatal(err)
	}
	header := map[string]string{}
	for _, field := range []string{"Source", "Fetched", "Entries", "Input SHA-256"} {
		pattern := regexp.MustCompile(`(?m)^// ` + regexp.QuoteMeta(field) + `: (.*)$`)
		match := pattern.FindSubmatch(generated)
		if match == nil {
			t.Fatalf("generated header has no %s line", field)
		}
		header[field] = string(match[1])
	}
	if header["Source"] != "https://speed.cloudflare.com/locations" {
		t.Fatalf("source is %q", header["Source"])
	}
	if header["Fetched"] != "2026-10-19" {
		t.Fatalf("fetch date is %q", header["Fetched"])
	}
	if header["Input SHA-256"] != digest {
		t.Fatalf("header hash %s does not hash the snapshot %s", header["Input SHA-256"], digest)
	}
	entries, err := strconv.Atoi(header["Entries"])
	if err != nil {
		t.Fatalf("entry count %q: %v", header["Entries"], err)
	}

	var snapshot []struct {
		IATA string  `json:"iata"`
		Lat  float64 `json:"lat"`
		Lon  float64 `json:"lon"`
		City string  `json:"city"`
	}
	if err := json.Unmarshal(raw, &snapshot); err != nil {
		t.Fatal(err)
	}
	colos := AllColos()
	if entries != len(snapshot) || entries != len(colos) || entries != 309 {
		t.Fatalf("header says %d entries, snapshot has %d, table has %d, want 309", entries, len(snapshot), len(colos))
	}
	byCode := map[string]Colo{}
	for _, c := range colos {
		byCode[c.Code] = c
	}
	for _, row := range snapshot {
		got, ok := byCode[row.IATA]
		if !ok {
			t.Fatalf("snapshot code %s was dropped", row.IATA)
		}
		if got.Lat != row.Lat || got.Lon != row.Lon || got.City != row.City {
			t.Fatalf("%s: table %+v, snapshot %+v", row.IATA, got, row)
		}
	}
	if len(byCode) != len(snapshot) {
		t.Fatalf("table repeats or invents a code: %d distinct of %d rows", len(byCode), len(snapshot))
	}

	for _, want := range []Colo{
		{Code: "NRT", Lat: 35.764702, Lon: 140.386002, City: "Tokyo"},
		{Code: "HKG", Lat: 22.308901, Lon: 113.915001, City: "Hong Kong"},
		{Code: "LAX", Lat: 33.942501, Lon: -118.407997, City: "Los Angeles"},
	} {
		got, ok := ColoAt(want.Code)
		if !ok || got != want {
			t.Fatalf("ColoAt(%q) = %+v, %v; want %+v", want.Code, got, ok, want)
		}
	}
	if _, ok := ColoAt("ZZZ"); ok {
		t.Fatal("an unknown code was found")
	}
	if _, ok := ColoAt("nrt"); ok {
		t.Fatal("a lowercase code was found")
	}
	if !sort.SliceIsSorted(colos, func(i, j int) bool { return colos[i].Code < colos[j].Code }) {
		t.Fatal("the table is not sorted by code")
	}
	// The returned slice is the caller's, so one reader cannot reorder the
	// table under another.
	colos[0].Code = "AAA"
	if got, _ := ColoAt("AAE"); got.Code != "AAE" {
		t.Fatal("AllColos handed out the table itself")
	}
}
