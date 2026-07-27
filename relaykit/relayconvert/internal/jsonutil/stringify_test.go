package jsonutil

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
)

// ToJSONString marshals via common.Marshal and, on marshal failure, falls back
// to fmt.Sprintf("%v", v). Both branches are covered here.

func TestToJSONString_MarshalsStruct(t *testing.T) {
	type payload struct {
		Name string `json:"name"`
		N    int    `json:"n"`
	}
	got := ToJSONString(payload{Name: "abc", N: 7})
	assert.JSONEq(t, `{"name":"abc","n":7}`, got)
}

func TestToJSONString_MarshalsScalarsAndMaps(t *testing.T) {
	assert.Equal(t, `"hello"`, ToJSONString("hello"))
	assert.Equal(t, `42`, ToJSONString(42))
	assert.Equal(t, `null`, ToJSONString(nil))
	assert.JSONEq(t, `{"k":"v"}`, ToJSONString(map[string]string{"k": "v"}))
}

func TestToJSONString_UnmarshalableFallsBackToSprintf(t *testing.T) {
	// Channels cannot be JSON marshaled -> error path -> Sprintf fallback.
	ch := make(chan int)
	got := ToJSONString(ch)
	assert.Equal(t, fmt.Sprintf("%v", ch), got)
	assert.NotEqual(t, "", got)
}
