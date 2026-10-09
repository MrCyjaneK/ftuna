// Package xzjson writes an indented JSON document and compresses it with xz.
package xzjson

import (
	"bytes"
	"encoding/json"

	"github.com/ulikunitz/xz"
)

func Encode(v any) ([]byte, error) {
	raw, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return nil, err
	}
	raw = append(raw, '\n')
	var buf bytes.Buffer
	w, err := xz.NewWriter(&buf)
	if err != nil {
		return nil, err
	}
	if _, err := w.Write(raw); err != nil {
		w.Close()
		return nil, err
	}
	if err := w.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
