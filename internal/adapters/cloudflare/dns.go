package cloudflare

import (
	"net/netip"
	"strconv"
	"strings"

	"github.com/WhitenWhiten/topophany/internal/adapters"
	"github.com/WhitenWhiten/topophany/internal/model"
)

// dnsResponse reads only the documented record fields this adapter reports.
// content is read to compare with the declared expectation and is never put
// in a fact or a detail: the observation keeps only whether it matched. The
// record id, zone name and other fields are absent from the struct.
type dnsResponse struct {
	Success *bool `json:"success"`
	// Result is a pointer so an absent or null result is distinguishable from
	// an explicitly empty list: the first is a malformed answer, the second is
	// the platform saying the declared record does not exist.
	Result *[]struct {
		Proxied *bool  `json:"proxied"`
		TTL     *int   `json:"ttl"`
		Content string `json:"content"`
	} `json:"result"`
	ResultInfo *struct {
		TotalCount *int `json:"total_count"`
	} `json:"result_info"`
}

// sameContent compares a declared record content with the one Cloudflare
// reports, in the form Cloudflare stores it rather than byte for byte: the
// platform lowercases a CNAME target and drops its trailing dot, and writes an
// address in its own canonical spelling, so what a person typed and what the
// API returns can differ while naming the same thing. An address that does not
// parse on either side is a mismatch. Neither value is returned or stored.
func sameContent(recordType, declared, reported string) bool {
	switch recordType {
	case "A", "AAAA":
		want, err := netip.ParseAddr(declared)
		if err != nil {
			return false
		}
		got, err := netip.ParseAddr(reported)
		return err == nil && want == got
	case "CNAME":
		return canonicalName(declared) == canonicalName(reported)
	}
	return declared == reported
}

// canonicalName is a host name as Cloudflare stores a CNAME target: lowercase,
// without the root label's trailing dot.
func canonicalName(name string) string {
	return strings.ToLower(strings.TrimSuffix(name, "."))
}

// collectDNSRecord turns the DNS list read into facts and declared-state
// policies. record_missing is policy rather than failure: the check declares
// which record it expects, and the API's answer that it is gone is the state
// the check exists to report, not a failed request.
func collectDNSRecord(body []byte, options model.CloudflareOptions, o *model.Observation) error {
	var doc dnsResponse
	if err := decodeJSON(body, &doc); err != nil {
		return err
	}
	if err := checkEnvelope(doc.Success); err != nil {
		return err
	}
	if doc.Result == nil {
		return adapters.Local("adapter_api", "unsupported_response_shape")
	}
	records := *doc.Result
	count := len(records)
	// A partial page is not the answer to "does this record exist": counts and
	// policies would be based on a subset. The documented list endpoint
	// paginates, so reject a total_count that does not equal this page.
	if info := doc.ResultInfo; info != nil && info.TotalCount != nil && *info.TotalCount != count {
		return adapters.Local("adapter_api", "unsupported_response_shape")
	}
	o.Outcome = "ok"
	o.Facts["record_count"] = strconv.Itoa(count)

	// A name and type can return more than one record. Report a proxied or
	// TTL value only when every record agrees on it; otherwise the fact is
	// omitted and record_count says there is more than one reading.
	var proxied *bool
	var ttl *int
	proxiedConsistent, ttlConsistent := true, true
	for _, record := range records {
		if record.Proxied == nil {
			proxiedConsistent = false
		} else if proxied == nil {
			value := *record.Proxied
			proxied = &value
		} else if *proxied != *record.Proxied {
			proxiedConsistent = false
		}
		if record.TTL == nil || *record.TTL < 0 {
			ttlConsistent = false
		} else if ttl == nil {
			value := *record.TTL
			ttl = &value
		} else if *ttl != *record.TTL {
			ttlConsistent = false
		}
	}
	if proxied != nil && proxiedConsistent {
		o.Facts["record_proxied"] = strconv.FormatBool(*proxied)
	}
	if ttl != nil && ttlConsistent {
		o.Facts["record_ttl"] = strconv.Itoa(*ttl)
	}

	matches := "not_declared"
	if options.ExpectedContent != "" {
		matches = "true"
		if count == 0 {
			matches = "false"
		}
		for _, record := range records {
			if !sameContent(options.RecordType, options.ExpectedContent, record.Content) {
				matches = "false"
			}
		}
	}
	o.Facts["record_content_matches"] = matches

	if count == 0 {
		o.Outcome, o.Code = "policy", "record_missing"
		o.Detail = "the declared DNS record is absent from the zone"
		return nil
	}
	if want := options.ExpectedProxied; want != nil {
		for _, record := range records {
			if record.Proxied == nil {
				return adapters.Local("adapter_api", "unsupported_response_shape")
			}
			if *record.Proxied != *want {
				if *want {
					o.Outcome, o.Code = "policy", "record_not_proxied"
					o.Detail = "the declared DNS record is no longer proxied by Cloudflare"
				} else {
					o.Outcome, o.Code = "policy", "record_proxied_unexpectedly"
					o.Detail = "the declared DNS record is proxied although the topology says it should not be"
				}
				return nil
			}
		}
	}
	if options.ExpectedContent != "" && matches == "false" {
		o.Outcome, o.Code = "policy", "record_content_mismatch"
		o.Detail = "the DNS record content differs from the declared value"
	}
	return nil
}
