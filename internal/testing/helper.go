package testing

import (
	"bytes"
	"errors"
)

type TestReducer struct{}

func (TestReducer) Reduce(_ string, bs [][]byte) ([]byte, int, error) {
	index := -1
	for i, b := range bs {
		if bytes.Equal(b, []byte("newer")) {
			index = i
		} else if bytes.Equal(b, []byte("valid")) {
			if index == -1 {
				index = i
			}
		}
	}
	if index == -1 {
		return nil, index, errors.New("no rec found")
	}
	return bs[index], index, nil
}
func (TestReducer) Validate(_ string, b []byte) error {
	if bytes.Equal(b, []byte("expired")) {
		return errors.New("expired")
	}
	return nil
}
