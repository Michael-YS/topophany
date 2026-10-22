package beszel

import "encoding/json"

// decodeObjectFields reads only exact protocol names. encoding/json's struct
// decoder also accepts case-insensitive matches, letting an unknown "ID" or
// "STATS" member overwrite canonical data. Unknown members stay ignored here;
// the caller validates required fields after this representation check.
// The entire response has already passed jsonBudget, including duplicate keys.
func decodeObjectFields(raw []byte, fields map[string]any) error {
	var object map[string]json.RawMessage
	if json.Unmarshal(raw, &object) != nil || object == nil {
		return failure("beszel_json")
	}
	for name, target := range fields {
		if value, present := object[name]; present {
			if json.Unmarshal(value, target) != nil {
				return failure("beszel_json")
			}
		}
	}
	return nil
}
