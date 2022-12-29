package reducer

import (
	"github.com/ipfs/go-ipns"
	record "github.com/libp2p/go-libp2p-record"
)

// Reducer is an interface that validates and reduces records
type Reducer interface {
	Validate(key string, value []byte) error
	// Reduce returns a value and a number indicating which of the input values it corresponds to
	// if it doesn't match any of the input values and is a new value it returns -1
	Reduce(key string, values [][]byte) ([]byte, int, error)
}

func select2Reduce(v record.Validator, key string, values [][]byte) ([]byte, int, error) {
	i, err := v.Select(key, values)
	if err != nil {
		return nil, i, err
	}
	return values[i], i, nil
}

type PublicKeyReducer struct {
	*record.PublicKeyValidator
}

func (r PublicKeyReducer) Reduce(key string, values [][]byte) ([]byte, int, error) {
	return select2Reduce(r, key, values)
}

type IpnsReducer struct {
	*ipns.Validator
}

func (r IpnsReducer) Reduce(key string, values [][]byte) ([]byte, int, error) {
	return select2Reduce(r, key, values)
}
