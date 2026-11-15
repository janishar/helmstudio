package lora

import (
	"bytes"
	"crypto/rand"
	"encoding/binary"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/helmstudio/studio_service/registry"
)

// fixtures is contracts/fixtures: the shared LoRA files (header only) and the canonical qwen-image lora block.
var fixtures = filepath.Join("..", "..", "..", "contracts", "fixtures")

// qwenBlock is the canonical qwen-image lora block.
func qwenBlock(t *testing.T) registry.LoraBlock { return familyBlock(t, "qwen-image") }

// familyBlock is the canonical lora block of a family (qwen-image, ltx).
func familyBlock(t *testing.T, family string) registry.LoraBlock {
	t.Helper()
	var models struct{ Models []registry.Model }
	data, err := os.ReadFile(filepath.Join(fixtures, "registry", family+".models.valid.json"))
	if err == nil {
		err = json.Unmarshal(data, &models)
	}
	if err != nil || models.Models[0].Lora == nil {
		t.Fatal(err)
	}
	return *models.Models[0].Lora
}

// fileTensor is a fixture's tensor as written.
type fileTensor struct {
	DType string  `json:"dtype"`
	Shape []int64 `json:"shape"`
}

// synth writes a safetensors file of the tensors (random data) and metadata, as a runner's index would read it.
func synth(t *testing.T, tensors map[string]fileTensor, metadata map[string]string) []byte {
	t.Helper()
	header := map[string]any{}
	if metadata != nil {
		header["__metadata__"] = metadata
	}
	var offset int64
	keys := make([]string, 0, len(tensors))
	for key := range tensors {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	for _, key := range keys {
		size := spanBytes(Tensor{DType: tensors[key].DType, Shape: tensors[key].Shape})
		header[key] = map[string]any{"dtype": tensors[key].DType, "shape": tensors[key].Shape, "data_offsets": []int64{offset, offset + size}}
		offset += size
	}
	text, _ := json.Marshal(header)
	for len(text)%8 != 0 {
		text = append(text, ' ')
	}
	out := binary.LittleEndian.AppendUint64(nil, uint64(len(text)))
	out = append(out, text...)
	data := make([]byte, offset)
	_, _ = rand.Read(data)
	return append(out, data...)
}

// read parses a whole file's header.
func read(file []byte) (Header, string) {
	header, reason, err := ReadHeader(bytes.NewReader(file), int64(len(file)))
	if err != nil {
		return Header{}, "read: " + err.Error()
	}
	return header, reason
}

// Every contracts/fixtures/loras file, written as a real safetensors file, normalises as check.py says; studio's
// upload checks pass the files whose only fault is a family fit, and name the first bad key of the others.
func TestLoraFixtures(t *testing.T) {
	block := qwenBlock(t)
	blocks := map[string]registry.LoraBlock{"qwen-image": block, "ltx": familyBlock(t, "ltx")}
	files, _ := filepath.Glob(filepath.Join(fixtures, "loras", "*.json"))
	if len(files) < 7 {
		t.Fatalf("found %d LoRA fixtures", len(files))
	}
	upload := map[string]string{ // the upload check's verdict (no published family ignores lora_te1_*)
		"kohya-te.json":         "lora_te1_text_model_encoder_layers_0_self_attn_k_proj.alpha is not a LoRA weight",
		"kohya-te-ignored.json": "lora_te1_text_model_encoder_layers_0_self_attn_k_proj.alpha is not a LoRA weight",
		"broken-pairs.json":     "transformer_blocks.0.attn.to_v: lora_B is missing",
	}
	for _, file := range files {
		t.Run(filepath.Base(file), func(t *testing.T) {
			var fixture struct {
				Name     string                `json:"name"`
				Family   string                `json:"family"`
				Metadata map[string]string     `json:"metadata"`
				Tensors  map[string]fileTensor `json:"tensors"`
				Ignore   []string              `json:"ignore"`
				Expect   struct {
					Result
					Rank    *int    `json:"rank"`
					Family  string  `json:"family"`
					Licence *string `json:"licence"`
				} `json:"expect"`
			}
			data, _ := os.ReadFile(file)
			if err := json.Unmarshal(data, &fixture); err != nil {
				t.Fatal(err)
			}
			header, reason := read(synth(t, fixture.Tensors, fixture.Metadata))
			if reason != "" {
				t.Fatal(reason)
			}
			family := cmpOr(fixture.Family, "qwen-image")
			spec := blocks[family]
			if fixture.Ignore != nil {
				spec.Ignore = fixture.Ignore
			}
			got := Normalise(fixture.Name, header, spec, family)
			want := fixture.Expect.Result
			want.Rank = 0
			if fixture.Expect.Rank != nil {
				want.Rank = *fixture.Expect.Rank
			}
			if want.Unapplied == nil {
				want.Unapplied = []string{}
			}
			if !reflect.DeepEqual(got, want) {
				t.Errorf("got %+v\nwant %+v", got, want)
			}
			detected := Detect(fixture.Name, header, map[string]registry.LoraBlock{family: spec})
			if detected != fixture.Expect.Family || !reflect.DeepEqual(Licence(header.Metadata), fixture.Expect.Licence) {
				t.Errorf("family %s licence %v", detected, Licence(header.Metadata))
			}
			style, rank, detail := Check(header, nil)
			if detail != upload[filepath.Base(file)] || (detail == "" && (style != want.Style || rank != want.Rank)) {
				t.Errorf("upload check: %q %s %d", detail, style, rank)
			}
		})
	}
	// with a family that declares the text-encoder keys harmless, the kohya-te file uploads and fits that family
	var fixture struct {
		Tensors map[string]fileTensor `json:"tensors"`
	}
	data, _ := os.ReadFile(filepath.Join(fixtures, "loras", "kohya-te.json"))
	_ = json.Unmarshal(data, &fixture)
	header, _ := read(synth(t, fixture.Tensors, nil))
	other := block
	other.Ignore = []string{"lora_te1_*"}
	blocks = map[string]registry.LoraBlock{"qwen-image": block, "zz-other": other}
	if _, _, detail := Check(header, Ignores(blocks)); detail != "" {
		t.Fatal(detail)
	}
	if family := Detect("te", header, blocks); family != "zz-other" {
		t.Fatal(family)
	}
}

// contracts/fixtures/loras/detect/families.json: a family a non-fake env publishes before a fake-only one, then by name.
func TestDetectFixture(t *testing.T) {
	var fx struct {
		Families map[string]struct {
			Envs  []string `json:"envs"`
			Block string   `json:"block"`
		} `json:"families"`
		Cases []struct {
			Lora     string   `json:"lora"`
			Families []string `json:"families"`
			Family   string   `json:"family"`
		} `json:"cases"`
	}
	data, err := os.ReadFile(filepath.Join(fixtures, "loras", "detect", "families.json"))
	if err == nil {
		err = json.Unmarshal(data, &fx)
	}
	if err != nil || len(fx.Cases) == 0 {
		t.Fatal(err)
	}
	for _, c := range fx.Cases {
		blocks := map[string]registry.LoraBlock{}
		for _, family := range c.Families {
			f := fx.Families[family]
			var models struct{ Models []registry.Model }
			data, _ := os.ReadFile(filepath.Join(fixtures, f.Block))
			if err := json.Unmarshal(data, &models); err != nil || models.Models[0].Lora == nil {
				t.Fatal(f.Block, err)
			}
			block := *models.Models[0].Lora
			block.Fake = !slices.ContainsFunc(f.Envs, func(e string) bool { return e != "fake" })
			blocks[family] = block
		}
		var lora struct {
			Name    string                `json:"name"`
			Tensors map[string]fileTensor `json:"tensors"`
		}
		data, _ := os.ReadFile(filepath.Join(fixtures, "loras", c.Lora))
		_ = json.Unmarshal(data, &lora)
		header, reason := read(synth(t, lora.Tensors, nil))
		if reason != "" {
			t.Fatal(reason)
		}
		if got := Detect(lora.Name, header, blocks); got != c.Family {
			t.Errorf("%s over %v: got %s, want %s", c.Lora, c.Families, got, c.Family)
		}
	}
}

// Malformed and hostile headers are refused with the contract's reason, reading no more than the header.
func TestReadHeader(t *testing.T) {
	good := map[string]fileTensor{"a.lora_A.weight": {"F16", []int64{4, 8}}, "a.lora_B.weight": {"F16", []int64{8, 4}}}
	withHeader := func(text string, data int) []byte {
		out := binary.LittleEndian.AppendUint64(nil, uint64(len(text)))
		return append(append(out, text...), make([]byte, data)...)
	}
	length := func(n uint64, rest int) []byte {
		return append(binary.LittleEndian.AppendUint64(nil, n), make([]byte, rest)...)
	}
	deep := `{"a": {"dtype": "F16", "shape": ` + strings.Repeat("[", 20000) + strings.Repeat("]", 20000) + `, "data_offsets": [0, 0]}}`
	var many strings.Builder
	many.WriteString("{")
	for i := range MaxTensors + 1 {
		if i > 0 {
			many.WriteString(",")
		}
		many.WriteString(`"t` + strconv.Itoa(i) + `": {"dtype": "F16", "shape": [0], "data_offsets": [0, 0]}`)
	}
	many.WriteString("}")
	for name, test := range map[string]struct {
		file []byte
		want string
	}{
		"zip":              {[]byte("PK\x03\x04rest of a zip"), "pickle checkpoints are not accepted; convert it to safetensors"},
		"pickle":           {[]byte{0x80, 0x04, 0x95, 0, 0, 0, 0, 0, 0, 0}, "pickle checkpoints are not accepted; convert it to safetensors"},
		"tiny":             {[]byte{1, 2, 3}, "not a safetensors file"},
		"length under 2":   {length(1, 10), "not a safetensors file"},
		"length past file": {length(1<<62, 10), "not a safetensors file"},
		"header over 8 MB": {length(MaxHeader+1, MaxHeader+1), "safetensors header is larger than 8 MB"},
		"not utf-8":        {withHeader("{\"\xff\": 1}", 0), "not a safetensors file"},
		"not an object":    {withHeader(`[1, 2]`, 0), "not a safetensors file"},
		"unknown field":    {withHeader(`{"a": {"dtype": "F16", "shape": [1], "data_offsets": [0, 2], "x": 1}}`, 2), "not a safetensors file"},
		"missing field":    {withHeader(`{"a": {"dtype": "F16", "shape": [1]}}`, 2), "not a safetensors file"},
		"duplicate key": {withHeader(`{"a": {"dtype": "F16", "shape": [1], "data_offsets": [0, 2]}, "a": {"dtype": "F16", "shape": [1], "data_offsets": [0, 2]}}`, 2),
			"not a safetensors file"},
		"metadata not strings": {withHeader(`{"__metadata__": {"a": 1}}`, 0), "not a safetensors file"},
		"negative shape":       {withHeader(`{"a": {"dtype": "F16", "shape": [-1], "data_offsets": [0, 2]}}`, 2), "not a safetensors file"},
		"deep nesting":         {withHeader(deep, 0), "not a safetensors file"},
		"trailing junk":        {withHeader(`{"a": {"dtype": "F16", "shape": [1], "data_offsets": [0, 2]}} x`, 2), "not a safetensors file"},
		"too many tensors":     {withHeader(many.String(), 0), "more than 20000 tensors"},
		"outside":              {withHeader(`{"a": {"dtype": "F16", "shape": [4], "data_offsets": [0, 8]}}`, 4), "tensor a lies outside the file"},
		"overlap": {withHeader(`{"a": {"dtype": "F16", "shape": [2], "data_offsets": [0, 4]}, "b": {"dtype": "F16", "shape": [2], "data_offsets": [2, 6]}}`, 6),
			"tensor a lies outside the file"},
		"span not its shape": {withHeader(`{"a": {"dtype": "F16", "shape": [4], "data_offsets": [0, 2]}}`, 2), "tensor a lies outside the file"},
		"huge shape":         {withHeader(`{"a": {"dtype": "F32", "shape": [4294967296, 4294967296], "data_offsets": [0, 2]}}`, 2), "tensor a lies outside the file"},
		"dtype":              {withHeader(`{"a": {"dtype": "I8", "shape": [2], "data_offsets": [0, 2]}}`, 2), "tensor a has dtype I8; LoRAs must be F32, F16 or BF16"},
		"good":               {synth(t, good, map[string]string{"license": " mit "}), ""},
	} {
		if _, got := read(test.file); got != test.want {
			t.Errorf("%s: %q, want %q", name, got, test.want)
		}
	}
	// the reader stops at the header: the tensor data is left for the hash
	file := synth(t, good, nil)
	r := bytes.NewReader(file)
	if _, _, err := ReadHeader(r, int64(len(file))); err != nil || r.Len() != 2*32*2 {
		t.Fatalf("left %d bytes: %v", r.Len(), err)
	}
}

// Check names the first failing rule after the header's.
func TestCheck(t *testing.T) {
	for name, test := range map[string]struct {
		tensors map[string]fileTensor
		want    string
	}{
		"full model": {map[string]fileTensor{"model.layers.0.weight": {"F16", []int64{4, 4}}}, "no LoRA keys found (is it a full model?)"},
		"stray key": {map[string]fileTensor{"a.lora_A.weight": {"F16", []int64{4, 8}}, "a.lora_B.weight": {"F16", []int64{8, 4}},
			"b.weight": {"F16", []int64{4}}}, "b.weight is not a LoRA weight"},
		"no A":        {map[string]fileTensor{"a.lora_B.weight": {"F16", []int64{8, 4}}}, "a: lora_A is missing"},
		"ranks":       {map[string]fileTensor{"a.lora_A.weight": {"F16", []int64{4, 8}}, "a.lora_B.weight": {"F16", []int64{8, 2}}}, "a: lora_A and lora_B ranks differ"},
		"rank > 4096": {map[string]fileTensor{"a.lora_A.weight": {"F16", []int64{4097, 1}}, "a.lora_B.weight": {"F16", []int64{1, 4097}}}, "rank 4097 is over 4096"},
	} {
		header, reason := read(synth(t, test.tensors, nil))
		if _, _, got := Check(header, nil); reason != "" || got != test.want {
			t.Errorf("%s: %q %q, want %q", name, reason, got, test.want)
		}
	}
}
