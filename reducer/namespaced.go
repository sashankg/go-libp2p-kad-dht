package reducer

import (
	"errors"

	record "github.com/libp2p/go-libp2p-record"
)

var ErrInvalidRecordType = errors.New("invalid record keytype")

type NamespacedReducer map[string]Reducer

// ReducerByKey looks up the validator responsible for validating the given
// key.
func (v NamespacedReducer) ReducerByKey(key string) Reducer {
	ns, _, err := record.SplitKey(key)
	if err != nil {
		return nil
	}
	return v[ns]
}

// Validate conforms to the Validator interface.
func (v NamespacedReducer) Validate(key string, value []byte) error {
	vi := v.ReducerByKey(key)
	if vi == nil {
		return ErrInvalidRecordType
	}
	return vi.Validate(key, value)
}

// Select conforms to the Validator interface.
func (v NamespacedReducer) Reduce(key string, values [][]byte) ([]byte, int, error) {
	if len(values) == 0 {
		return nil, -1, errors.New("can't select from no values")
	}
	vi := v.ReducerByKey(key)
	if vi == nil {
		return nil, -1, ErrInvalidRecordType
	}
	return vi.Reduce(key, values)
}

var _ Reducer = NamespacedReducer{}
