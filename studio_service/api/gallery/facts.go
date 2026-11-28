package gallery

import "encoding/json"

// FactsOut is a take's facts as GET /takes/{id}, studio.see and look_at answer them (openapi TakeFacts): the facts
// with the grid's signed link (null for audio) and its frames' seconds; nil when there are none yet.
func FactsOut(raw json.RawMessage, sign func(string) string) map[string]any {
	var f struct {
		Facts json.RawMessage `json:"facts"`
		Key   *string         `json:"key"`
		At    []float64       `json:"at"`
	}
	if len(raw) == 0 || string(raw) == "null" || json.Unmarshal(raw, &f) != nil {
		return nil
	}
	out := map[string]any{}
	if json.Unmarshal(f.Facts, &out) != nil {
		return nil
	}
	out["grid_url"], out["at"] = nil, f.At
	if f.At == nil {
		out["at"] = []float64{}
	}
	if f.Key != nil && sign != nil {
		out["grid_url"] = sign(*f.Key)
	}
	return out
}
