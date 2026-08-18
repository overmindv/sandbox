package objectstore

import "context"

type DisabledStore struct{}

func (DisabledStore) Provider() string {
	return "disabled"
}

func (DisabledStore) Put(context.Context, Object) (StoredObject, error) {
	return StoredObject{}, ErrDisabled
}

func (DisabledStore) Get(context.Context, string, string) ([]byte, error) {
	return nil, ErrDisabled
}

func (DisabledStore) Close() error {
	return nil
}
