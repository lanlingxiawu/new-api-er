package common

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"

	kitutil "github.com/QuantumNous/new-api/relaykit/relayconvert/kitutil"
	"github.com/gin-gonic/gin/binding"
)

type RawMessage = json.RawMessage

// hostJSONCodec is the single place where the host chooses its JSON engine.
// Swap the implementation here (for example to sonic.ConfigStd) and every
// common.* and kitutil.* JSON helper, including relaykit DTO (un)marshalling,
// follows. Injected from init() rather than main() so tests run on the same
// engine as production: common is imported by virtually every root package
// and test binary, while main() never executes under `go test`.
type hostJSONCodec struct{}

func (hostJSONCodec) Marshal(v any) ([]byte, error) {
	return json.Marshal(v)
}

func (hostJSONCodec) Unmarshal(data []byte, v any) error {
	return json.Unmarshal(data, v)
}

func (hostJSONCodec) Decode(r io.Reader, v any) error {
	return json.NewDecoder(r).Decode(v)
}

// jsonDocumentReader retains read errors that Decoder can defer after decoding
// a complete value from a Read returning both bytes and an error.
type jsonDocumentReader struct {
	io.Reader
	err        error
	emptyReads int
}

func (r *jsonDocumentReader) Read(p []byte) (int, error) {
	if r.err != nil {
		return 0, r.err
	}
	n, err := r.Reader.Read(p)
	if n == 0 && err == nil {
		r.emptyReads++
		if r.emptyReads >= 100 {
			err = io.ErrNoProgress
		}
	} else {
		r.emptyReads = 0
	}
	r.err = err
	return n, err
}

func (hostJSONCodec) DecodeSingle(r io.Reader, v any) error {
	reader := &jsonDocumentReader{Reader: r}
	decoder := json.NewDecoder(reader)
	if err := decoder.Decode(v); err != nil {
		return err
	}
	if reader.err != nil && reader.err != io.EOF {
		return reader.err
	}
	// Decoder may have read past the first value. Inspect its buffer before the
	// remaining input, without decoding or allocating a second JSON document.
	tail := io.MultiReader(decoder.Buffered(), reader)
	var buffer [4096]byte
	for {
		n, err := tail.Read(buffer[:])
		for _, b := range buffer[:n] {
			switch b {
			case ' ', '\t', '\r', '\n':
			default:
				return errors.New("unexpected data after JSON document")
			}
		}
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
	}
}

func (hostJSONCodec) Valid(data []byte) bool {
	return json.Valid(data)
}

func init() {
	kitutil.SetCodec(hostJSONCodec{})
}

func Unmarshal(data []byte, v any) error {
	return kitutil.Unmarshal(data, v)
}

func UnmarshalJsonStr(data string, v any) error {
	return kitutil.UnmarshalJsonStr(data, v)
}

func DecodeJson(reader io.Reader, v any) error {
	return kitutil.DecodeJson(reader, v)
}

// DecodeSingleJson requires exactly one JSON document followed by JSON
// whitespace and EOF. DecodeJson intentionally retains its streaming contract.
func DecodeSingleJson(reader io.Reader, v any) error {
	return (hostJSONCodec{}).DecodeSingle(reader, v)
}

// DecodeJsonWithValidation decodes JSON and applies Gin's configured binding-tag
// validator, including binding:"required" and any registered custom validators.
func DecodeJsonWithValidation(reader io.Reader, v any) error {
	if err := DecodeJson(reader, v); err != nil {
		return err
	}
	if binding.Validator == nil {
		return nil
	}
	return binding.Validator.ValidateStruct(v)
}

func Marshal(v any) ([]byte, error) {
	return kitutil.Marshal(v)
}

func IndentJson(data []byte) ([]byte, error) {
	var buffer bytes.Buffer
	if err := json.Indent(&buffer, data, "", "  "); err != nil {
		return nil, err
	}
	return buffer.Bytes(), nil
}

func GetJsonType(data RawMessage) string {
	return kitutil.GetJsonType(data)
}

// JsonRawMessageToString returns JSON strings as their decoded value and other JSON values as raw text.
func JsonRawMessageToString(data RawMessage) string {
	return kitutil.JsonRawMessageToString(data)
}
