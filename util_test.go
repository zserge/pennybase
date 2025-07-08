package pennybase

import (
	"os"
	"testing"
)

type mustResult[T any] struct {
	Val T
	Err error
}

func (m mustResult[T]) T(t *testing.T) T {
	t.Helper()
	must0(t, m.Err)
	return m.Val
}

func must[T any](v T, err error) mustResult[T] { return mustResult[T]{v, err} }

func must0(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}
}

func testData(t *testing.T, src string) string {
	t.Helper()
	dst := t.TempDir()
	must0(t, os.CopyFS(dst, os.DirFS(src)))
	return dst
}

func textValue(s string) Value        { return Value{Type: Text, Text: s} }
func numberValue(n float64) Value     { return Value{Type: Number, Number: n} }
func listValue(items ...string) Value { return Value{Type: List, List: items} }
