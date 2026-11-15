// Package lora: the LoRA library (contracts/README.md "LoRAs"): a user's own LoRAs (table loras, uploaded by the takes'
// signed PUT and checked at complete), the shared ones the live hosts index, the header parser and the normaliser
// studio detects style, rank and family with, and the lookups run creation and release use.
package lora

import (
	"bytes"
	"cmp"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"math/bits"
	"slices"
	"sort"
	"unicode/utf8"
)

// Bounds of a file studio reads (contracts/README.md "LoRA upload checks").
const (
	MaxHeader  = 8388608 // bytes of the JSON header
	MaxTensors = 20000
	MaxRank    = 4096
)

// Tensor is one header entry; Begin and End are offsets into the data after the header.
type Tensor struct {
	DType string
	Shape []int64
	Begin int64
	End   int64
}

// Header is a safetensors file's header: its tensors by key and its __metadata__ strings.
type Header struct {
	Tensors  map[string]Tensor
	Metadata map[string]string
}

// Keys are the tensor keys in code-point order.
func (h Header) Keys() []string {
	keys := make([]string, 0, len(h.Tensors))
	for key := range h.Tensors {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

// dtypeBytes is the element size of each safetensors dtype.
var dtypeBytes = map[string]int64{"F64": 8, "F32": 4, "F16": 2, "BF16": 2, "I64": 8, "I32": 4, "I16": 2, "I8": 1,
	"U64": 8, "U32": 4, "U16": 2, "U8": 1, "BOOL": 1, "F8_E4M3": 1, "F8_E5M2": 1}

const notSafetensors = "not a safetensors file"

// ReadHeader reads a safetensors header from the first bytes of a file of size bytes. It reads exactly the 8-byte
// length and the header (at most MaxHeader), never tensor data, so r can go on to hash the rest. The reason is the
// first failed upload check up to the dtypes (the 422 detail, "" when the header is good); err is a read failure.
func ReadHeader(r io.Reader, size int64) (Header, string, error) {
	head := make([]byte, min(8, max(size, 0)))
	if _, err := io.ReadFull(r, head); err != nil {
		return Header{}, "", err
	}
	if bytes.HasPrefix(head, []byte("PK\x03\x04")) || (len(head) >= 2 && head[0] == 0x80 && head[1] >= 2 && head[1] <= 5) {
		return Header{}, "pickle checkpoints are not accepted; convert it to safetensors", nil
	}
	if len(head) < 8 {
		return Header{}, notSafetensors, nil
	}
	n := binary.LittleEndian.Uint64(head)
	if n < 2 || n > uint64(size-8) {
		return Header{}, notSafetensors, nil
	}
	if n > MaxHeader {
		return Header{}, "safetensors header is larger than 8 MB", nil
	}
	text := make([]byte, n)
	if _, err := io.ReadFull(r, text); err != nil {
		return Header{}, "", err
	}
	header, ok := parseHeader(text)
	if !ok {
		return Header{}, notSafetensors, nil
	}
	if len(header.Tensors) > MaxTensors {
		return Header{}, fmt.Sprintf("more than %d tensors", MaxTensors), nil
	}
	return header, checkTensors(header, size-8-int64(n)), nil
}

// entry is a header entry as written; pointers tell a missing field from a zero one.
type entry struct {
	DType       *string  `json:"dtype"`
	Shape       *[]int64 `json:"shape"`
	DataOffsets *[]int64 `json:"data_offsets"`
}

// parseHeader decodes a header: UTF-8, one JSON object (padding spaces after it allowed) of {dtype, shape,
// data_offsets} entries and an optional __metadata__ of strings, no key twice, no other field. Go's decoder bounds
// nesting (10000 levels) and every value is decoded into a fixed shape, so a hostile header fails fast within its
// 8 MB.
func parseHeader(text []byte) (Header, bool) {
	if !utf8.Valid(text) {
		return Header{}, false
	}
	decoder := json.NewDecoder(bytes.NewReader(text))
	decoder.DisallowUnknownFields()
	if token, err := decoder.Token(); err != nil || token != json.Delim('{') {
		return Header{}, false
	}
	header := Header{Tensors: map[string]Tensor{}, Metadata: map[string]string{}}
	metadata := false
	for decoder.More() {
		token, err := decoder.Token()
		key, isKey := token.(string)
		if err != nil || !isKey {
			return Header{}, false
		}
		if _, dup := header.Tensors[key]; dup || (key == "__metadata__" && metadata) {
			return Header{}, false
		}
		if key == "__metadata__" {
			metadata = true
			if decoder.Decode(&header.Metadata) != nil || header.Metadata == nil {
				return Header{}, false
			}
			continue
		}
		var e entry
		if decoder.Decode(&e) != nil || e.DType == nil || e.Shape == nil || e.DataOffsets == nil || len(*e.DataOffsets) != 2 ||
			slices.ContainsFunc(*e.Shape, func(d int64) bool { return d < 0 }) {
			return Header{}, false
		}
		header.Tensors[key] = Tensor{DType: *e.DType, Shape: *e.Shape, Begin: (*e.DataOffsets)[0], End: (*e.DataOffsets)[1]}
	}
	if token, err := decoder.Token(); err != nil || token != json.Delim('}') {
		return Header{}, false
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) { // only padding may follow
		return Header{}, false
	}
	return header, true
}

// checkTensors checks the offsets (inside the data, no two tensors overlapping, the span the shape's size) and the
// dtypes, each the first failing key in code-point order.
func checkTensors(header Header, data int64) string {
	keys := header.Keys()
	for _, key := range keys {
		if t := header.Tensors[key]; t.Begin < 0 || t.Begin > t.End || t.End > data {
			return "tensor " + key + " lies outside the file"
		}
	}
	byBegin := slices.Clone(keys)
	slices.SortStableFunc(byBegin, func(a, b string) int {
		ta, tb := header.Tensors[a], header.Tensors[b]
		return cmp.Or(cmp.Compare(ta.Begin, tb.Begin), cmp.Compare(ta.End, tb.End))
	})
	var overlapping []string
	end, last := int64(0), ""
	for _, key := range byBegin {
		t := header.Tensors[key]
		if t.Begin == t.End {
			continue // empty: takes no bytes
		}
		if t.Begin < end {
			overlapping = append(overlapping, min(key, last))
		}
		if t.End > end {
			end, last = t.End, key
		}
	}
	if len(overlapping) > 0 {
		return "tensor " + slices.Min(overlapping) + " lies outside the file"
	}
	for _, key := range keys {
		if t := header.Tensors[key]; t.DType != "F32" && t.DType != "F16" && t.DType != "BF16" {
			return "tensor " + key + " has dtype " + t.DType + "; LoRAs must be F32, F16 or BF16"
		}
	}
	for _, key := range keys {
		if t := header.Tensors[key]; t.End-t.Begin != spanBytes(t) {
			return "tensor " + key + " lies outside the file" // its span is not what its shape takes
		}
	}
	return ""
}

// spanBytes is how many bytes a tensor's shape takes, -1 when it overflows.
func spanBytes(t Tensor) int64 {
	n := uint64(dtypeBytes[t.DType])
	for _, d := range t.Shape {
		hi, lo := bits.Mul64(n, uint64(d))
		if hi != 0 || lo > math.MaxInt64 {
			return -1
		}
		n = lo
	}
	return int64(n)
}
