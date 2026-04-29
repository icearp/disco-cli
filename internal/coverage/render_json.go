package coverage

import (
	"encoding/json"
	"io"
)

// RenderJSON emits a structured matrix slice for tooling. Shape mirrors the
// in-memory Matrix/Row types directly so consumers can decode into the same
// structs.
func RenderJSON(w io.Writer, matrices []Matrix) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(matrices)
}
